package tcpctl

import (
	"testing"
	"time"
)

// tanpaJitter membuat deret backoff deterministik untuk diperiksa.
func tanpaJitter(d time.Duration) time.Duration { return d }

func TestBackoffTumbuhEksponensialLaluMentok(t *testing.T) {
	b := Backoff{
		Min:    500 * time.Millisecond,
		Max:    15 * time.Second,
		Factor: 2,
		Jitter: tanpaJitter,
	}
	want := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		15 * time.Second, // mentok di Max
		15 * time.Second,
		15 * time.Second,
	}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("percobaan %d: Next = %v, want %v", i+1, got, w)
		}
	}
}

func TestBackoffResetSetelahKoneksiBerhasil(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 10 * time.Second, Factor: 2, Jitter: tanpaJitter}
	for i := 0; i < 5; i++ {
		b.Next()
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("setelah Reset: Next = %v, want 1s", got)
	}
}

func TestBackoffPakaiBawaanUntukNilaiTakSah(t *testing.T) {
	b := Backoff{Jitter: tanpaJitter} // seluruhnya nol
	if got := b.Next(); got != DefaultBackoffMin {
		t.Fatalf("Next = %v, want %v", got, DefaultBackoffMin)
	}

	// Max lebih kecil dari Min tidak boleh menghasilkan deret menurun.
	c := Backoff{Min: 5 * time.Second, Max: time.Second, Jitter: tanpaJitter}
	if got := c.Next(); got != 5*time.Second {
		t.Fatalf("Max<Min: Next = %v, want 5s", got)
	}
}

// Deret yang sudah mentok tidak boleh meluap walau dipanggil sangat sering.
func TestBackoffTidakMeluapSetelahBanyakPercobaan(t *testing.T) {
	b := Backoff{Min: time.Millisecond, Max: time.Second, Factor: 2, Jitter: tanpaJitter}
	for i := 0; i < 10_000; i++ {
		got := b.Next()
		if got != time.Second && got > time.Second {
			t.Fatalf("percobaan %d: Next = %v, melewati Max", i, got)
		}
		if got <= 0 {
			t.Fatalf("percobaan %d: Next = %v, tidak boleh <= 0", i, got)
		}
	}
}

// Jitter bawaan harus menyebar jeda tanpa membuatnya runtuh ke nol: satu lahan penuh
// gerbang yang putus bersamaan tidak boleh dial ulang serempak.
func TestJitterBawaanBeradaDiSetengahAtas(t *testing.T) {
	b := Backoff{Min: time.Second, Max: time.Second, Factor: 2}

	adaVariasi := false
	pertama := b.Next()
	for i := 0; i < 200; i++ {
		got := b.Next()
		if got < 500*time.Millisecond || got > time.Second {
			t.Fatalf("jitter = %v, di luar rentang [500ms, 1s]", got)
		}
		if got != pertama {
			adaVariasi = true
		}
	}
	if !adaVariasi {
		t.Fatal("jitter tidak menghasilkan variasi sama sekali")
	}
}

func TestEqualJitterMenanganiDurasiKecil(t *testing.T) {
	if got := equalJitter(0); got != 0 {
		t.Fatalf("equalJitter(0) = %v, want 0", got)
	}
	if got := equalJitter(-time.Second); got != 0 {
		t.Fatalf("equalJitter(negatif) = %v, want 0", got)
	}
	if got := equalJitter(1); got != 1 {
		t.Fatalf("equalJitter(1ns) = %v, want 1ns", got)
	}
}
