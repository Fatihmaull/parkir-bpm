package tcpctl

import (
	"sync"
	"time"
)

// DefaultTelemetryCap — jumlah entri TX/RX yang disimpan per device.
const DefaultTelemetryCap = 256

// Severity menandai bobot satu entri telemetry (PRD v3 §6.1: audit bila ≥ warning).
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return "info"
	}
}

// Direction membedakan arah lalu lintas dari sudut pandang Edge.
type Direction int

const (
	DirTX Direction = iota // Edge → controller
	DirRX                  // controller → Edge
)

func (d Direction) String() string {
	if d == DirTX {
		return "TX"
	}
	return "RX"
}

// TelemetryEntry adalah satu baris jejak komunikasi perangkat.
type TelemetryEntry struct {
	At       time.Time
	Dir      Direction
	Payload  string // muatan ASCII frame; kosong untuk kejadian non-frame
	Severity Severity
	Note     string // keterangan anomali; kosong bila normal
}

// Ring adalah penyangga melingkar berukuran tetap: entri terlama tergusur sendiri.
//
// Ukuran tetap adalah syarat, bukan pilihan — Edge berjalan berbulan-bulan tanpa
// diawasi, dan jejak komunikasi yang tumbuh bebas akan menghabiskan memori lahan.
type Ring struct {
	mu     sync.Mutex
	buf    []TelemetryEntry
	next   int
	terisi int
}

// NewRing membuat penyangga berkapasitas cap. Nilai <= 0 memakai DefaultTelemetryCap.
func NewRing(cap int) *Ring {
	if cap <= 0 {
		cap = DefaultTelemetryCap
	}
	return &Ring{buf: make([]TelemetryEntry, cap)}
}

// Add menyimpan satu entri, menggusur yang terlama bila penuh.
func (r *Ring) Add(e TelemetryEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.terisi < len(r.buf) {
		r.terisi++
	}
}

// Snapshot mengembalikan salinan isi penyangga, urut dari terlama ke terbaru.
func (r *Ring) Snapshot() []TelemetryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]TelemetryEntry, 0, r.terisi)
	mulai := 0
	if r.terisi == len(r.buf) {
		mulai = r.next // sudah melingkar: yang tertua ada di posisi tulis berikutnya
	}
	for i := 0; i < r.terisi; i++ {
		out = append(out, r.buf[(mulai+i)%len(r.buf)])
	}
	return out
}

// Len melaporkan jumlah entri yang tersimpan.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terisi
}

// Cap melaporkan kapasitas penyangga.
func (r *Ring) Cap() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}

// ── Integrasi Device ──

// WithTelemetryCap mengatur kapasitas ring buffer TX/RX.
func WithTelemetryCap(n int) DeviceOption {
	return func(d *Device) {
		if n > 0 {
			d.telemetryCap = n
		}
	}
}

// WithTelemetryKeepalive menentukan apakah lalu lintas keepalive ikut dicatat.
//
// Bawaannya tidak. PING/PINGOK menghasilkan dua entri per detik; membiarkannya masuk
// akan memenuhi penyangga dalam hitungan menit dan mengubur justru kejadian yang
// ingin dilihat teknisi. Nyalakan hanya saat mendiagnosa keepalive itu sendiri.
func WithTelemetryKeepalive(ikut bool) DeviceOption {
	return func(d *Device) { d.telemetryKeepalive = ikut }
}

// WithAuditHook memasang penerima entri ber-severity >= warning (PRD v3 §6.1).
//
// Hook dipanggil dari goroutine driver, jadi ia WAJIB cepat dan tidak memblokir;
// penulisan ke basis data harus diantre di sisi pemanggil.
func WithAuditHook(h func(TelemetryEntry)) DeviceOption {
	return func(d *Device) { d.auditHook = h }
}

// Telemetry mengembalikan jejak TX/RX terakhir, urut dari terlama ke terbaru.
func (d *Device) Telemetry() []TelemetryEntry { return d.telemetry.Snapshot() }

// catat menyimpan satu entri dan meneruskannya ke audit bila cukup berat.
func (d *Device) catat(dir Direction, payload string, sev Severity, note string) {
	if !d.telemetryKeepalive && (payload == CmdPing || payload == RespPingOK) && sev == SeverityInfo {
		return
	}
	e := TelemetryEntry{At: time.Now(), Dir: dir, Payload: payload, Severity: sev, Note: note}
	d.telemetry.Add(e)

	if sev >= SeverityWarning && d.auditHook != nil {
		d.auditHook(e)
	}
}
