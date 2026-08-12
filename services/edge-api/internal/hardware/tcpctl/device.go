package tcpctl

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// ErrNotConnected dikembalikan bila perintah dikirim saat controller sedang terputus.
//
// Perintah TIDAK diantre diam-diam. Menahan lalu mengirimkannya belakangan berbahaya:
// `OUT1OFF` yang tertunda beberapa detik bisa menutup palang saat kendaraan lain sudah
// berada di bawahnya. Antrian perintah yang sadar-konteks adalah task 3.3.
var ErrNotConnected = errors.New("tcpctl: controller sedang terputus")

// Bawaan keepalive aplikasi (PRD v3 §6.1: PING tiap 1 dtk, OFFLINE setelah 3× gagal).
const (
	DefaultPingInterval  = 1 * time.Second
	DefaultMaxMissedPing = 3
	defaultStatusBuffer  = 16
)

// DeviceStatus menggambarkan kelayakan pakai satu controller.
type DeviceStatus int

const (
	// StatusDisconnected — tidak ada koneksi TCP (sedang dial atau menunggu backoff).
	StatusDisconnected DeviceStatus = iota
	// StatusOnline — tersambung dan membalas PING.
	StatusOnline
	// StatusUnresponsive — socket TCP masih hidup tetapi controller bisu: PING tak
	// dibalas sebanyak ambang. Status transien; koneksi langsung diputus paksa agar
	// supervisor menyambung ulang.
	StatusUnresponsive
)

func (s DeviceStatus) String() string {
	switch s {
	case StatusOnline:
		return "ONLINE"
	case StatusUnresponsive:
		return "UNRESPONSIVE"
	default:
		return "DISCONNECTED"
	}
}

// Online melaporkan apakah controller layak menerima perintah.
func (s DeviceStatus) Online() bool { return s == StatusOnline }

// DeviceStats mencatat riwayat koneksi satu controller — bahan halaman Hardware dan
// alert operasional (task 1.8 & 3.4).
type DeviceStats struct {
	Connects      uint64 // dial yang berhasil
	DialFailures  uint64 // dial yang gagal
	Disconnects   uint64 // koneksi yang sudah terbentuk lalu putus
	PingsSent     uint64 // PING yang berhasil ditulis ke socket
	PingFailures  uint64 // PING yang gagal ditulis
	Pongs         uint64 // PINGOK yang diterima
	Unresponsive  uint64 // koneksi diputus paksa karena PING tak dibalas
	StatusDropped uint64 // perubahan status yang hilang karena konsumen lambat

	Commands        uint64 // Exec yang berhasil mendapat balasan
	CommandRetries  uint64 // pengulangan kirim karena balasan tak kunjung datang
	CommandTimeouts uint64 // percobaan yang habis waktu menunggu balasan
}

// Device adalah koneksi yang menyembuhkan diri sendiri ke satu controller gerbang
// A6/A9 (PRD v3 §6.1: koneksi persisten + auto-reconnect dengan backoff & jitter,
// keepalive PING 1 dtk, tandai OFFLINE setelah 3× gagal).
//
// Berbeda dari Client yang mewakili satu koneksi dan kanalnya tertutup saat putus,
// kanal Frames() milik Device bertahan melintasi reconnect dan baru tertutup saat
// Device dihentikan. Dengan begitu lapisan di atasnya (state machine gerbang) tidak
// perlu tahu-menahu soal siklus koneksi.
//
// Yang BELUM ditangani di sini, sesuai pembagian task: korelasi perintah↔respons
// (1.4) dan resync STAT setelah pulih (3.2). Konsekuensi OFFLINE terhadap state
// machine — gerbang ke FAULT, lampu merah, alert critical (PRD v3 §6.1 "watchdog per
// device") — dipasang di lapisan gatesvc, bukan di driver ini.
//
// Setelah reconnect, status fisik palang & loop belum tentu sama dengan sebelum putus;
// pemanggil tidak boleh menganggap state lamanya masih berlaku.
type Device struct {
	addr string

	backoff       Backoff
	dialTimeout   time.Duration
	writeTimeout  time.Duration
	keepAlive     time.Duration
	frameBuffer   int
	pingInterval  time.Duration
	maxMissedPing int
	ackTimeout    time.Duration
	maxAttempts   int
	guard         func(cmd string) error

	debounceWindow time.Duration
	debounce       *Debouncer
	loopEvents     chan hw.LoopEvent

	frames   chan string
	statusCh chan DeviceStatus
	done     chan struct{}
	wg       sync.WaitGroup

	// missed dihitung oleh goroutine keepalive dan di-nol-kan oleh goroutine penyalur
	// frame saat PINGOK tiba, jadi harus atomik.
	missed atomic.Int32

	// execMu menyerialkan Exec: protokol A6/A9 serial per koneksi, jadi hanya satu
	// perintah boleh menunggu balasan pada satu waktu.
	execMu sync.Mutex

	pendMu sync.Mutex
	pend   *penungguAck

	mu      sync.Mutex
	client  *Client
	status  DeviceStatus
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

// WithPingInterval mengatur jarak antar-PING. Nilai <= 0 mematikan keepalive aplikasi.
//
// Mematikannya adalah katup darurat, bukan mode normal: §5.5 & §13 PRD v3 mencatat
// semantik STAT (dan karenanya kepatuhan firmware) masih menunggu konfirmasi vendor.
// Bila controller di lapangan ternyata tidak membalas PING sesuai kontrak §5.3,
// keepalive dapat dimatikan sementara agar driver tidak memutus koneksi yang sehat.
func WithPingInterval(t time.Duration) DeviceOption {
	return func(d *Device) { d.pingInterval = t }
}

// WithMaxMissedPing mengatur berapa PING beruntun tanpa PINGOK sebelum controller
// dianggap bisu. Nilai < 1 dikembalikan ke bawaan.
func WithMaxMissedPing(n int) DeviceOption {
	return func(d *Device) {
		if n >= 1 {
			d.maxMissedPing = n
		}
	}
}

// WithDebounce mengatur jendela stabilitas input. Nilai <= 0 memakai DefaultDebounce.
func WithDebounce(t time.Duration) DeviceOption {
	return func(d *Device) { d.debounceWindow = t }
}

// NewDevice menyiapkan Device untuk controller di addr. Koneksi baru dimulai saat Start.
func NewDevice(addr string, opts ...DeviceOption) *Device {
	d := &Device{
		addr:          addr,
		dialTimeout:   defaultDialTimeout,
		writeTimeout:  defaultWriteTimeout,
		keepAlive:     defaultKeepAlive,
		frameBuffer:   defaultFrameBuffer,
		pingInterval:  DefaultPingInterval,
		maxMissedPing: DefaultMaxMissedPing,
		ackTimeout:    DefaultAckTimeout,
		maxAttempts:   DefaultMaxAttempts,
		status:        StatusDisconnected,
	}
	for _, o := range opts {
		o(d)
	}
	d.frames = make(chan string, d.frameBuffer)
	d.statusCh = make(chan DeviceStatus, defaultStatusBuffer)
	d.loopEvents = make(chan hw.LoopEvent, d.frameBuffer)
	d.done = make(chan struct{})
	d.debounce = NewDebouncer(d.debounceWindow, d.pancarkanLoop)
	return d
}

// LoopEvents mengalirkan perubahan input yang SUDAH ter-debounce, dalam bentuk
// hw.LoopEvent — inilah yang dikonsumsi state machine gerbang.
//
// Frames() tetap membawa frame mentah termasuk INxON/INxOFF yang belum disaring; itu
// pandangan diagnostik untuk halaman Hardware & telemetry (task 1.8), tempat teknisi
// lapangan justru ingin melihat pantulan yang dibuang. Dua pandangan itu sengaja
// dibedakan: yang satu apa adanya, yang satu sudah layak dipercaya.
//
// LoopEvent.LoopID saat ini berisi nomor kanal input (1..4) apa adanya. Pemetaannya ke
// LD1..LD4 per gerbang mengikuti gates.config dan merupakan task 1.9.
//
// Event tidak pernah dibuang diam-diam: bila konsumen lambat, penyaring akan menahan
// diri sampai event diterima atau Device berhenti.
func (d *Device) LoopEvents() <-chan hw.LoopEvent { return d.loopEvents }

// pancarkanLoop meneruskan event yang sudah stabil ke konsumen.
func (d *Device) pancarkanLoop(ev hw.LoopEvent) {
	select {
	case d.loopEvents <- ev:
	case <-d.done:
	}
}

// Addr mengembalikan alamat controller.
func (d *Device) Addr() string { return d.addr }

// Frames mengembalikan kanal frame masuk. Kanal ini bertahan melintasi reconnect dan
// hanya tertutup setelah Device berhenti (lewat Close atau pembatalan ctx).
//
// Frame PINGOK TIDAK diteruskan ke sini: itu housekeeping 1 Hz yang hanya berarti bagi
// pelacak kesehatan di bawah, dan meneruskannya cuma jadi derau bagi state machine.
func (d *Device) Frames() <-chan string { return d.frames }

// StatusChanges mengalirkan setiap perubahan status controller. Kanal ditutup bersama
// Frames() saat Device berhenti.
//
// Pengiriman bersifat non-blocking: konsumen yang lambat akan kehilangan perubahan, dan
// kehilangan itu tercatat di DeviceStats.StatusDropped. Sumber kebenaran status yang
// sesungguhnya tetap Status().
func (d *Device) StatusChanges() <-chan DeviceStatus { return d.statusCh }

// Status mengembalikan status controller saat ini.
func (d *Device) Status() DeviceStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

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

// Connected melaporkan apakah saat ini ada koneksi TCP hidup ke controller. Perhatikan
// bahwa tersambung belum tentu responsif — pakai Status() untuk kelayakan pakai.
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
		// Supervisor tak pernah jalan, jadi tak ada yang akan menutup kanal.
		d.debounce.Stop()
		close(d.loopEvents)
		close(d.frames)
		close(d.statusCh)
	}
	return nil
}

// supervise menjaga satu koneksi tetap hidup: dial, salurkan frame sampai putus,
// tunggu sesuai backoff, ulangi.
func (d *Device) supervise(ctx context.Context) {
	defer d.wg.Done()
	defer func() {
		// Hentikan penyaring lebih dulu: Stop menjamin tak ada emit menyusul, sehingga
		// menutup loopEvents sesudahnya aman.
		d.debounce.Stop()
		close(d.loopEvents)
		close(d.statusCh)
		close(d.frames)
	}()

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

		// Optimistis: dianggap sehat sampai PING membuktikan sebaliknya (P2 —
		// ketersediaan mengalahkan kesempurnaan data).
		d.missed.Store(0)
		d.setStatus(StatusOnline)
		d.backoff.Reset()

		ctxKoneksi, batalKoneksi := context.WithCancel(ctx)
		var kw sync.WaitGroup
		kw.Add(1)
		go func() {
			defer kw.Done()
			d.keepalive(ctxKoneksi, c)
		}()

		d.salurkan(ctxKoneksi, c)

		batalKoneksi()
		// Tutup dulu, baru tunggu: menutup socket membebaskan PING yang mungkin
		// sedang menggantung di Write, alih-alih menunggu writeTimeout habis.
		_ = c.Close()
		kw.Wait()

		d.mu.Lock()
		d.client = nil
		d.stats.Disconnects++
		d.mu.Unlock()
		d.setStatus(StatusDisconnected)

		// Selama Edge buta, kendaraan bisa datang atau pergi — keyakinan lama tentang
		// posisi loop tak lagi berdasar. Lihat Debouncer.Reset.
		d.debounce.Reset()

		if d.stopping(ctx) {
			return
		}
		if !d.tunggu(ctx, d.backoff.Next()) {
			return
		}
	}
}

// keepalive mengirim PING berkala dan memutus koneksi yang tak kunjung dibalas.
//
// Memutus paksa adalah inti gunanya keepalive aplikasi: TCP yang setengah terbuka
// (controller hang, socket masih "established") bisa bertahan menit-menit sebelum
// keep-alive socket menyadarinya. Selama itu Edge mengira palang bisa diperintah,
// padahal tidak. Lebih baik diputus supaya supervisor menyambung ulang.
func (d *Device) keepalive(ctx context.Context, c *Client) {
	if d.pingInterval <= 0 {
		return // keepalive aplikasi dimatikan
	}

	t := time.NewTicker(d.pingInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			if err := c.Send(ctx, CmdPing); err != nil {
				// Gagal menulis dihitung sebagai satu kesempatan yang hilang, sama
				// seperti PING yang tak dibalas.
				d.note(func(s *DeviceStats) { s.PingFailures++ })
			} else {
				d.note(func(s *DeviceStats) { s.PingsSent++ })
			}

			if int(d.missed.Add(1)) >= d.maxMissedPing {
				d.note(func(s *DeviceStats) { s.Unresponsive++ })
				d.setStatus(StatusUnresponsive)
				_ = c.Close() // paksa putus → supervisor men-dial ulang
				return
			}
		case <-ctx.Done():
			return
		case <-d.done:
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
			if !d.amati(f) {
				continue
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

// amati memperbarui pelacakan kesehatan dari satu frame masuk, menyerahkannya ke
// perintah yang sedang menunggu balasan, dan melaporkan apakah frame itu perlu
// diteruskan ke konsumen.
//
// Hanya PINGOK yang me-nol-kan pencacah kesehatan, bukan sembarang lalu lintas masuk.
// Kontrak §5.3 menyebut PING↔PINGOK secara khusus, dan controller yang hang masih
// mungkin memuntahkan event lama dari buffer-nya — itu bukan bukti hidup.
func (d *Device) amati(f string) bool {
	if f == RespPingOK {
		d.missed.Store(0)
		d.note(func(s *DeviceStats) { s.Pongs++ })
	}
	// Pemeriksaan penunggu tetap dijalankan untuk PINGOK, supaya Exec(ctx, CmdPing)
	// yang dipakai untuk probe manual tetap mendapat balasannya.
	if d.serahkanAck(f) {
		return false
	}

	// Event input mentah masuk penyaring; yang keluar dari sana adalah LoopEvent.
	// Frame mentahnya tetap diteruskan ke Frames() sebagai bahan diagnostik.
	if ch, high, ok := ParseInput(f); ok {
		d.debounce.Observe(ch, high)
	}

	return f != RespPingOK // PINGOK tak pernah jadi konsumsi state machine
}

// setStatus mencatat status baru dan menyiarkannya bila berubah.
func (d *Device) setStatus(s DeviceStatus) {
	d.mu.Lock()
	if d.status == s {
		d.mu.Unlock()
		return
	}
	d.status = s
	d.mu.Unlock()

	select {
	case d.statusCh <- s:
	default:
		d.note(func(st *DeviceStats) { st.StatusDropped++ })
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
