package tcpctl

import (
	"math"
	"math/rand"
	"time"
)

// Bawaan backoff reconnect (PRD v3 §6.1: "0.5s→1s→2s→5s→maks 15s").
const (
	DefaultBackoffMin    = 500 * time.Millisecond
	DefaultBackoffMax    = 15 * time.Second
	DefaultBackoffFactor = 2.0
)

// Backoff menghitung jeda antar-percobaan dial ulang ke controller.
//
// Jeda tumbuh eksponensial lalu ditahan di Max, dan selalu diberi jitter. Jitter
// bukan hiasan: satu switch atau UPS yang mati membuat SELURUH gerbang di lahan
// terputus bersamaan, dan tanpa jitter semuanya akan dial ulang pada detik yang
// sama berulang kali.
//
// Backoff tidak aman dipakai lintas goroutine; Device memakainya dari goroutine
// supervisor saja.
type Backoff struct {
	Min    time.Duration
	Max    time.Duration
	Factor float64

	// Jitter menyebar jeda. nil berarti equal jitter: setengah tetap + setengah acak,
	// sehingga jeda tak pernah runtuh mendekati nol tapi tetap ter-dekorelasi.
	Jitter func(time.Duration) time.Duration

	n int // jumlah kenaikan yang sudah dipakai; berhenti tumbuh setelah mentok di Max
}

// Next mengembalikan jeda untuk percobaan berikutnya dan memajukan deret.
func (b *Backoff) Next() time.Duration {
	min, max, factor := b.Min, b.Max, b.Factor
	if min <= 0 {
		min = DefaultBackoffMin
	}
	if max <= 0 {
		max = DefaultBackoffMax
	}
	if factor < 1 {
		factor = DefaultBackoffFactor
	}
	if max < min {
		max = min
	}

	d := float64(min) * math.Pow(factor, float64(b.n))
	if d >= float64(max) || math.IsInf(d, 0) {
		d = float64(max)
		// n sengaja tidak dinaikkan lagi setelah mentok: deret sudah di Max selamanya,
		// dan membiarkannya tumbuh hanya membuat math.Pow meluap.
	} else {
		b.n++
	}

	jitter := b.Jitter
	if jitter == nil {
		jitter = equalJitter
	}
	return jitter(time.Duration(d))
}

// Reset mengembalikan deret ke Min. Dipanggil setiap kali koneksi berhasil, supaya
// gangguan berikutnya dimulai dari jeda pendek lagi.
func (b *Backoff) Reset() { b.n = 0 }

// equalJitter mengembalikan nilai acak di rentang [d/2, d].
func equalJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(rand.Int63n(int64(half)+1)) //nolint:gosec // jitter jadwal, bukan kripto
}
