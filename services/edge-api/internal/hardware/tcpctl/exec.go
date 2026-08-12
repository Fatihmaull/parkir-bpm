package tcpctl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Bawaan korelasi perintah↔respons (PRD v3 §6.1: "kirim OUT1ON, tunggu OUT1ONOK
// dengan timeout + retry 3×").
const (
	// DefaultAckTimeout — batas menunggu balasan satu perintah. Controller berada di
	// LAN yang sama dan protokolnya sepele, jadi balasan wajar datang dalam milidetik;
	// setengah detik sudah sangat longgar tanpa membuat palang terasa lamban.
	DefaultAckTimeout = 500 * time.Millisecond

	// DefaultMaxAttempts — jumlah percobaan TOTAL, bukan tambahan setelah percobaan
	// pertama. "retry 3×" di PRD dibaca sebagai tiga kali kirim lalu menyerah.
	DefaultMaxAttempts = 3
)

// ErrAckTimeout — controller tidak membalas perintah dalam batas waktu.
var ErrAckTimeout = errors.New("tcpctl: controller tidak membalas perintah")

// penungguAck menautkan satu perintah yang sedang menunggu balasannya.
type penungguAck struct {
	cocok func(string) bool
	ch    chan string
}

// WithAckTimeout mengatur batas menunggu balasan satu perintah.
func WithAckTimeout(t time.Duration) DeviceOption {
	return func(d *Device) {
		if t > 0 {
			d.ackTimeout = t
		}
	}
}

// WithMaxAttempts mengatur jumlah percobaan kirim sebelum menyerah. Nilai < 1
// dikembalikan ke bawaan.
func WithMaxAttempts(n int) DeviceOption {
	return func(d *Device) {
		if n >= 1 {
			d.maxAttempts = n
		}
	}
}

// WithCommandGuard memasang pemeriksaan yang dijalankan SEBELUM SETIAP percobaan
// kirim, termasuk tiap pengulangan. Mengembalikan error berarti perintah dibatalkan.
//
// Ini ada karena retry menciptakan bahaya yang tidak dimiliki pengiriman sekali jalan:
// satu Exec bisa merentang ratusan milidetik, dan kondisi lapangan dapat berubah di
// tengahnya. `OUT1OFF` yang aman saat percobaan pertama bisa menjadi tidak aman pada
// percobaan ketiga bila loop di bawah palang keburu HIGH — persis yang dilarang
// interlock P4. Penegakan interlock yang sesungguhnya adalah task 2.3; yang disediakan
// di sini hanya titik sisipnya, supaya 2.3 tidak perlu membongkar ulang gelang retry.
func WithCommandGuard(g func(cmd string) error) DeviceOption {
	return func(d *Device) { d.guard = g }
}

// Exec mengirim perintah lalu menunggu balasannya, mengulang sampai maxAttempts.
//
// Protokol A6/A9 bersifat serial per koneksi (PRD v3 §6.1), jadi Exec menyerialkan
// dirinya sendiri: hanya satu perintah menunggu balasan pada satu waktu per Device.
//
// Exec TIDAK mengulang saat controller terputus — lihat ErrNotConnected di device.go
// untuk alasannya. Yang diulang hanya perintah yang terkirim tetapi tak dibalas.
//
// Balasan yang datang terlambat (setelah Exec menyerah) tidak lagi dikenali sebagai
// balasan dan akan muncul di Frames() sebagai frame biasa; konsumen Layer-2 (task 1.7)
// mengabaikan frame yang tak dikenal.
func (d *Device) Exec(ctx context.Context, cmd string) (string, error) {
	if err := ValidateCommand(cmd); err != nil {
		return "", err
	}

	d.execMu.Lock()
	defer d.execMu.Unlock()

	cocok := pencocokAck(cmd)
	var terakhir error

	for percobaan := 1; percobaan <= d.maxAttempts; percobaan++ {
		if g := d.commandGuard(); g != nil {
			if err := g(cmd); err != nil {
				return "", fmt.Errorf("tcpctl: perintah %s dibatalkan penjaga: %w", cmd, err)
			}
		}

		if percobaan > 1 {
			d.note(func(s *DeviceStats) { s.CommandRetries++ })
		}

		ack, err := d.sekaliKirim(ctx, cmd, cocok)
		if err == nil {
			d.note(func(s *DeviceStats) { s.Commands++ })
			return ack, nil
		}
		terakhir = err

		// Hanya kegagalan "terkirim tapi tak dibalas" yang layak diulang. Terputus,
		// dibatalkan, atau Device sudah ditutup: mengulang tak akan menolong.
		if !errors.Is(err, ErrAckTimeout) {
			break
		}
	}

	return "", fmt.Errorf("tcpctl: perintah %s gagal setelah %d percobaan: %w", cmd, d.maxAttempts, terakhir)
}

// sekaliKirim melakukan satu percobaan: pasang penunggu, kirim, tunggu balasan.
func (d *Device) sekaliKirim(ctx context.Context, cmd string, cocok func(string) bool) (string, error) {
	ch := make(chan string, 1)

	d.pendMu.Lock()
	d.pend = &penungguAck{cocok: cocok, ch: ch}
	d.pendMu.Unlock()
	defer func() {
		d.pendMu.Lock()
		d.pend = nil
		d.pendMu.Unlock()
	}()

	if err := d.Send(ctx, cmd); err != nil {
		return "", err
	}

	t := time.NewTimer(d.ackTimeout)
	defer t.Stop()

	select {
	case ack := <-ch:
		return ack, nil
	case <-t.C:
		d.note(func(s *DeviceStats) { s.CommandTimeouts++ })
		d.catat(DirTX, cmd, SeverityWarning, "balasan tidak datang dalam batas waktu")
		return "", ErrAckTimeout
	case <-ctx.Done():
		return "", ctx.Err()
	case <-d.done:
		return "", ErrClosed
	}
}

// serahkanAck menyerahkan frame ke perintah yang sedang menunggu, bila cocok.
// Mengembalikan true bila frame sudah dikonsumsi dan tak perlu diteruskan.
func (d *Device) serahkanAck(f string) bool {
	d.pendMu.Lock()
	p := d.pend
	d.pendMu.Unlock()

	if p == nil || !p.cocok(f) {
		return false
	}
	select {
	case p.ch <- f:
	default: // penunggu sudah pergi; frame dibiarkan lewat
		return false
	}
	return true
}

// pencocokAck membangun pencocok balasan untuk satu perintah.
//
// Kontrak §5.3: tiap perintah dibalas dirinya sendiri + "OK" (`OUT1ON` → `OUT1ONOK`),
// KECUALI `STAT` yang dibalas `STATabcdefgh` — bukan `STATOK`.
func pencocokAck(cmd string) func(string) bool {
	if cmd == CmdStat {
		return func(f string) bool {
			return len(f) > len(PrefixStatAck) && strings.HasPrefix(f, PrefixStatAck)
		}
	}
	want := cmd + SuffixAck
	return func(f string) bool { return f == want }
}
