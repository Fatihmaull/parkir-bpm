package tcpctl

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotConnected dikembalikan bila perintah dikirim saat controller sedang terputus.
//
// Perintah TIDAK diantre diam-diam. Menahan lalu mengirimkannya belakangan berbahaya:
// `OUT1OFF` yang tertunda beberapa detik bisa menutup palang saat kendaraan lain sudah
// berada di bawahnya. Antrian perintah yang sadar-konteks adalah task 3.3.
var ErrNotConnected = errors.New("tcpctl: controller sedang terputus")

// DeviceStats mencatat riwayat koneksi satu controller — bahan halaman Hardware dan
// alert operasional (task 1.8 & 3.4).
type DeviceStats struct {
	Connects     uint64 // dial yang berhasil
	DialFailures uint64 // dial yang gagal
	Disconnects  uint64 // koneksi yang sudah terbentuk lalu putus
}

// Device adalah koneksi yang menyembuhkan diri sendiri ke satu controller gerbang
// A6/A9 (PRD v3 §6.1: koneksi persisten + auto-reconnect dengan backoff & jitter).
//
// Berbeda dari Client yang mewakili satu koneksi dan kanalnya tertutup saat putus,
// kanal Frames() milik Device bertahan melintasi reconnect dan baru tertutup saat
// Device dihentikan. Dengan begitu lapisan di atasnya (state machine gerbang) tidak
// perlu tahu-menahu soal siklus koneksi.
//
// Yang BELUM ditangani di sini, sesuai pembagian task: penandaan OFFLINE lewat PING
// aplikasi (1.3), korelasi perintah↔respons (1.4), dan resync STAT setelah pulih (3.2).
// Setelah reconnect, status fisik palang & loop belum tentu sama dengan sebelum putus —
// pemanggil tidak boleh menganggap state lamanya masih berlaku.
type Device struct {
	addr string

	backoff      Backoff
	dialTimeout  time.Duration
	writeTimeout time.Duration
	keepAlive    time.Duration
	frameBuffer  int

	frames chan string
	done   chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	client  *Client
	started bool
	closed  bool
	stats   DeviceStats
}

// DeviceOption menyetel perilaku Device.
type DeviceOption func(*Device)

// WithReconnectBackoff mengganti parameter backoff reconnect.
func WithReconnectBackoff(b Backoff) DeviceOption {
	return func(d *Device) { d.backoff = b }
}

// WithDeviceDialTimeout membatasi lama satu upaya dial.
func WithDeviceDialTimeout(t time.Duration) DeviceOption {
	return func(d *Device) { d.dialTimeout = t }
}

// WithDeviceWriteTimeout membatasi lama satu operasi tulis.
func WithDeviceWriteTimeout(t time.Duration) DeviceOption {
	return func(d *Device) { d.writeTimeout = t }
}

// WithDeviceKeepAlive mengatur periode TCP keep-alive socket.
func WithDeviceKeepAlive(t time.Duration) DeviceOption {
	return func(d *Device) { d.keepAlive = t }
}

// WithDeviceFrameBuffer mengatur kedalaman antrian frame masuk.
func WithDeviceFrameBuffer(n int) DeviceOption {
	return func(d *Device) {
		if n > 0 {
			d.frameBuffer = n
		}
	}
}

// NewDevice menyiapkan Device untuk controller di addr. Koneksi baru dimulai saat Start.
func NewDevice(addr string, opts ...DeviceOption) *Device {
	d := &Device{
		addr:         addr,
		dialTimeout:  defaultDialTimeout,
		writeTimeout: defaultWriteTimeout,
		keepAlive:    defaultKeepAlive,
		frameBuffer:  defaultFrameBuffer,
	}
	for _, o := range opts {
		o(d)
	}
	d.frames = make(chan string, d.frameBuffer)
	d.done = make(chan struct{})
	return d
}

// Addr mengembalikan alamat controller.
func (d *Device) Addr() string { return d.addr }

// Frames mengembalikan kanal frame masuk. Kanal ini bertahan melintasi reconnect dan
// hanya tertutup setelah Device berhenti (lewat Close atau pembatalan ctx).
func (d *Device) Frames() <-chan string { return d.frames }

// Start menjalankan goroutine supervisor yang menjaga koneksi tetap hidup. Tidak
// memblokir. Pemanggilan kedua tidak melakukan apa-apa.
func (d *Device) Start(ctx context.Context) {
	d.mu.Lock()
	if d.started || d.closed {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.mu.Unlock()

	d.wg.Add(1)
	go d.supervise(ctx)
}

// Connected melaporkan apakah saat ini ada koneksi hidup ke controller.
func (d *Device) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.client != nil
}

// Stats mengembalikan salinan riwayat koneksi.
func (d *Device) Stats() DeviceStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

// Send mengirim satu perintah ke controller. Mengembalikan ErrNotConnected bila
// controller sedang terputus — pemanggil yang memutuskan apakah aman mengulang.
func (d *Device) Send(ctx context.Context, cmd string) error {
	d.mu.Lock()
	c, closed := d.client, d.closed
	d.mu.Unlock()

	switch {
	case closed:
		return ErrClosed
	case c == nil:
		return ErrNotConnected
	}
	return c.Send(ctx, cmd)
}

// Close menghentikan supervisor, memutus koneksi, dan menunggu goroutine-nya selesai.
// Aman dipanggil berkali-kali.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	c, started := d.client, d.started
	d.mu.Unlock()

	close(d.done)
	if c != nil {
		_ = c.Close()
	}
	if started {
		d.wg.Wait()
	} else {
		// Supervisor tak pernah jalan, jadi tak ada yang akan menutup kanal frames.
		close(d.frames)
	}
	return nil
}

// supervise menjaga satu koneksi tetap hidup: dial, salurkan frame sampai putus,
// tunggu sesuai backoff, ulangi.
func (d *Device) supervise(ctx context.Context) {
	defer d.wg.Done()
	defer close(d.frames)

	for {
		if d.stopping(ctx) {
			return
		}

		c, err := Dial(ctx, d.addr,
			WithDialTimeout(d.dialTimeout),
			WithWriteTimeout(d.writeTimeout),
			WithKeepAlive(d.keepAlive),
			WithFrameBuffer(d.frameBuffer),
		)
		if err != nil {
			d.note(func(s *DeviceStats) { s.DialFailures++ })
			if !d.tunggu(ctx, d.backoff.Next()) {
				return
			}
			continue
		}

		d.mu.Lock()
		// Close bisa saja menang balapan dengan dial yang baru selesai.
		if d.closed {
			d.mu.Unlock()
			_ = c.Close()
			return
		}
		d.client = c
		d.stats.Connects++
		d.mu.Unlock()

		// Koneksi berhasil → deret backoff dimulai ulang untuk gangguan berikutnya.
		d.backoff.Reset()

		d.salurkan(ctx, c)

		d.mu.Lock()
		d.client = nil
		d.stats.Disconnects++
		d.mu.Unlock()
		_ = c.Close()

		if d.stopping(ctx) {
			return
		}
		if !d.tunggu(ctx, d.backoff.Next()) {
			return
		}
	}
}

// salurkan meneruskan frame dari satu koneksi ke kanal Device sampai koneksi berakhir.
func (d *Device) salurkan(ctx context.Context, c *Client) {
	for {
		select {
		case f, ok := <-c.Frames():
			if !ok {
				return // koneksi mati
			}
			select {
			case d.frames <- f:
			case <-ctx.Done():
				return
			case <-d.done:
				return
			}
		case <-ctx.Done():
			return
		case <-d.done:
			return
		}
	}
}

// tunggu menahan selama dur; mengembalikan false bila Device keburu dihentikan.
func (d *Device) tunggu(ctx context.Context, dur time.Duration) bool {
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	case <-d.done:
		return false
	}
}

func (d *Device) stopping(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case <-d.done:
		return true
	default:
		return false
	}
}

func (d *Device) note(f func(*DeviceStats)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(&d.stats)
}
