package tcpctl

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// Nilai bawaan klien. Interval PING aplikasi dan ambang OFFLINE belum ada di sini —
// itu ranah task 1.3.
const (
	defaultDialTimeout  = 3 * time.Second
	defaultWriteTimeout = 2 * time.Second
	defaultFrameBuffer  = 128
	readChunkSize       = 512

	// defaultKeepAlive — periode TCP keep-alive socket. Ini jaring pengaman lapisan
	// transport untuk mendeteksi kabel tercabut / peer hilang tanpa FIN; deteksi cepat
	// yang sesungguhnya memakai PING aplikasi tiap 1 dtk (task 1.3).
	defaultKeepAlive = 15 * time.Second
)

// ErrClosed dikembalikan bila klien dipakai setelah Close.
var ErrClosed = errors.New("tcpctl: klien sudah ditutup")

// Client adalah koneksi persisten ke satu controller gerbang A6/A9 (PRD v3 §5.1:
// device bertindak sebagai TCP server, Edge sebagai client).
//
// Satu Client memiliki satu goroutine pembaca. Frame masuk dikirim ke kanal Frames()
// dan TIDAK PERNAH dibuang diam-diam saat konsumen lambat — kanal akan menahan
// pembaca, karena kehilangan event loop bawah berarti kehilangan dasar interlock (P4).
//
// Client sengaja hanya mewakili SATU koneksi dan tidak men-dial ulang sendiri: bila
// Frames() tertutup, koneksi itu sudah mati. Pemakaian normal di lapangan lewat Device,
// yang menyupervisi Client dan menyambung ulang dengan backoff.
type Client struct {
	addr string

	mu     sync.Mutex
	conn   net.Conn
	closed bool

	pmu    sync.Mutex // menjaga parser: ditulis readLoop, dibaca Stats()
	parser *Parser

	frames chan string
	done   chan struct{}

	errMu sync.Mutex
	err   error

	dialTimeout  time.Duration
	writeTimeout time.Duration
	keepAlive    time.Duration
	frameBuffer  int
}

// Option menyetel perilaku Client saat Dial.
type Option func(*Client)

// WithDialTimeout membatasi lama upaya membuka koneksi.
func WithDialTimeout(d time.Duration) Option {
	return func(c *Client) { c.dialTimeout = d }
}

// WithWriteTimeout membatasi lama satu operasi tulis ke socket.
func WithWriteTimeout(d time.Duration) Option {
	return func(c *Client) { c.writeTimeout = d }
}

// WithKeepAlive mengatur periode TCP keep-alive socket. Nilai <= 0 mematikannya.
func WithKeepAlive(d time.Duration) Option {
	return func(c *Client) { c.keepAlive = d }
}

// WithFrameBuffer mengatur kedalaman antrian frame masuk.
func WithFrameBuffer(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.frameBuffer = n
		}
	}
}

// Dial membuka koneksi ke controller pada addr ("ip:port", bawaan port 56001 per
// PRD v3 §5.1) dan menjalankan goroutine pembaca.
func Dial(ctx context.Context, addr string, opts ...Option) (*Client, error) {
	c := &Client{
		addr:         addr,
		parser:       NewParser(),
		done:         make(chan struct{}),
		dialTimeout:  defaultDialTimeout,
		writeTimeout: defaultWriteTimeout,
		keepAlive:    defaultKeepAlive,
		frameBuffer:  defaultFrameBuffer,
	}
	for _, o := range opts {
		o(c)
	}

	// KeepAlive pada net.Dialer menyalakan SO_KEEPALIVE sekaligus menyetel periodenya.
	d := &net.Dialer{Timeout: c.dialTimeout, KeepAlive: c.keepAlive}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.frames = make(chan string, c.frameBuffer)

	go c.readLoop()
	return c, nil
}

// Addr mengembalikan alamat controller.
func (c *Client) Addr() string { return c.addr }

// Frames mengembalikan kanal frame masuk. Kanal ditutup saat koneksi berakhir —
// baik karena Close() maupun karena kegagalan socket (periksa Err() untuk membedakan).
func (c *Client) Frames() <-chan string { return c.frames }

// Send membungkus cmd menjadi frame A6/A9 lalu menuliskannya ke socket.
//
// Tidak ada penantian balasan `…OK` di sini: korelasi perintah↔respons berikut retry
// adalah task 1.4.
func (c *Client) Send(ctx context.Context, cmd string) error {
	frame, err := Encode(cmd)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}

	deadline := time.Now().Add(c.writeTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if _, err := c.conn.Write(frame); err != nil {
		return err
	}
	return nil
}

// Close memutus koneksi dan menghentikan goroutine pembaca. Aman dipanggil berkali-kali.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	c.mu.Unlock()

	close(c.done)
	return conn.Close()
}

// Err melaporkan penyebab berhentinya goroutine pembaca. nil berarti koneksi masih
// hidup atau ditutup secara sengaja lewat Close().
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Stats mengembalikan pencacah parser (frame valid, byte sampah, frame terpotong, dst.).
func (c *Client) Stats() ParserStats {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	return c.parser.Stats()
}

func (c *Client) readLoop() {
	defer close(c.frames)

	buf := make([]byte, readChunkSize)
	for {
		n, err := c.conn.Read(buf)
		if n > 0 {
			for _, cmd := range c.extract(buf[:n]) {
				select {
				case c.frames <- cmd:
				case <-c.done:
					return
				}
			}
		}
		if err != nil {
			if !c.isClosed() {
				c.setErr(err)
			}
			return
		}
	}
}

// extract memasukkan potongan byte ke parser dan mengambil semua frame utuh yang
// sudah lengkap. Dipisah agar penguncian parser tetap sempit dan tidak menahan kunci
// selama pengiriman ke kanal.
func (c *Client) extract(chunk []byte) []string {
	c.pmu.Lock()
	defer c.pmu.Unlock()

	c.parser.Feed(chunk)
	var out []string
	for {
		cmd, ok := c.parser.Next()
		if !ok {
			return out
		}
		out = append(out, cmd)
	}
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Client) setErr(err error) {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err == nil {
		c.err = err
	}
}
