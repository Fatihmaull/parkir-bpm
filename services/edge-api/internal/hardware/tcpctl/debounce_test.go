package tcpctl

import (
	"context"
	"testing"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

func TestParseInput(t *testing.T) {
	sah := []struct {
		f    string
		ch   int
		high bool
	}{
		{"IN1ON", 1, true},
		{"IN2ON", 2, true},
		{"IN4OFF", 4, false},
		{"IN3OFF", 3, false},
	}
	for _, tc := range sah {
		ch, high, ok := ParseInput(tc.f)
		if !ok {
			t.Fatalf("ParseInput(%q) ok=false, want true", tc.f)
		}
		if ch != tc.ch || high != tc.high {
			t.Fatalf("ParseInput(%q) = (%d,%v), want (%d,%v)", tc.f, ch, high, tc.ch, tc.high)
		}
	}

	// Bukan event input — harus dibiarkan lewat, bukan disalahartikan.
	takSah := []string{
		"", "IN", "INON", "IN1", "IN0ON", "IN5ON", "IN12ON",
		"OUT1ON", "TRIG1", "PINGOK", "STAT10001000",
		"IN1ONOK", // balasan perintah, bukan event
		"W1A2B3C",
	}
	for _, f := range takSah {
		if _, _, ok := ParseInput(f); ok {
			t.Fatalf("ParseInput(%q) ok=true, want false", f)
		}
	}
}

// tampung mengumpulkan event yang dipancarkan penyaring.
type tampung struct {
	ch chan hw.LoopEvent
}

func penampung() *tampung { return &tampung{ch: make(chan hw.LoopEvent, 32)} }

func (p *tampung) emit(ev hw.LoopEvent) { p.ch <- ev }

func (p *tampung) tunggu(t *testing.T, batas time.Duration) hw.LoopEvent {
	t.Helper()
	select {
	case ev := <-p.ch:
		return ev
	case <-time.After(batas):
		t.Fatal("timeout menunggu LoopEvent")
		return hw.LoopEvent{}
	}
}

func (p *tampung) takAdaEvent(t *testing.T, selama time.Duration) {
	t.Helper()
	select {
	case ev := <-p.ch:
		t.Fatalf("ada LoopEvent tak terduga: %+v", ev)
	case <-time.After(selama):
	}
}

func TestDebouncerMengakuiNilaiYangBertahan(t *testing.T) {
	p := penampung()
	d := NewDebouncer(30*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(2, true)
	ev := p.tunggu(t, 2*time.Second)

	if ev.LoopID != 2 || !ev.High {
		t.Fatalf("event = %+v, want LoopID=2 High=true", ev)
	}
	if ev.TS == 0 {
		t.Fatal("TS tidak diisi")
	}
	if high, tahu := d.Stable(2); !tahu || !high {
		t.Fatalf("Stable(2) = (%v,%v), want (true,true)", high, tahu)
	}
}

func TestDebouncerBelumMengakuiSebelumJendelaLewat(t *testing.T) {
	p := penampung()
	d := NewDebouncer(200*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(1, true)
	p.takAdaEvent(t, 50*time.Millisecond)

	if _, tahu := d.Stable(1); tahu {
		t.Fatal("Stable(1) sudah diketahui padahal jendela belum lewat")
	}
}

// Pantulan kontak: nilai berubah-ubah cepat, hanya nilai akhir yang boleh diakui.
func TestDebouncerMenyaringPantulan(t *testing.T) {
	p := penampung()
	d := NewDebouncer(60*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	for i := 0; i < 6; i++ {
		d.Observe(1, true)
		time.Sleep(5 * time.Millisecond)
		d.Observe(1, false)
		time.Sleep(5 * time.Millisecond)
	}
	d.Observe(1, true) // nilai akhir yang bertahan

	ev := p.tunggu(t, 2*time.Second)
	if !ev.High {
		t.Fatalf("event = %+v, want High=true", ev)
	}
	p.takAdaEvent(t, 150*time.Millisecond)
}

// Inti keselamatan: pantulan yang kembali ke nilai lama tak boleh melahirkan tepi
// palsu. Satu tepi TURUN palsu pada loop bawah bisa menutup palang di atas kendaraan.
func TestDebouncerPantulanKembaliKeNilaiLamaTidakMemancarkanApaPun(t *testing.T) {
	p := penampung()
	d := NewDebouncer(80*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(2, true)
	if ev := p.tunggu(t, 2*time.Second); !ev.High {
		t.Fatalf("event awal = %+v, want High=true", ev)
	}

	// Kedip singkat ke LOW lalu kembali HIGH sebelum jendela habis.
	d.Observe(2, false)
	time.Sleep(10 * time.Millisecond)
	d.Observe(2, true)

	p.takAdaEvent(t, 250*time.Millisecond)

	if high, tahu := d.Stable(2); !tahu || !high {
		t.Fatalf("Stable(2) = (%v,%v), want tetap (true,true)", high, tahu)
	}
}

func TestDebouncerPembacaanBerulangHanyaSekaliDiakui(t *testing.T) {
	p := penampung()
	d := NewDebouncer(30*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	for i := 0; i < 5; i++ {
		d.Observe(1, true)
	}
	if ev := p.tunggu(t, 2*time.Second); !ev.High {
		t.Fatalf("event = %+v, want High=true", ev)
	}

	// Penegasan ulang nilai yang sudah diakui tidak melahirkan event baru.
	d.Observe(1, true)
	d.Observe(1, true)
	p.takAdaEvent(t, 150*time.Millisecond)
}

func TestDebouncerKanalSalingBebas(t *testing.T) {
	p := penampung()
	d := NewDebouncer(30*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(1, true)
	d.Observe(2, false)
	d.Observe(3, true)

	lihat := map[int]bool{}
	for i := 0; i < 3; i++ {
		ev := p.tunggu(t, 2*time.Second)
		lihat[ev.LoopID] = ev.High
	}
	if len(lihat) != 3 || !lihat[1] || lihat[2] || !lihat[3] {
		t.Fatalf("event per kanal = %v, want {1:true, 2:false, 3:true}", lihat)
	}
}

func TestDebouncerResetMelupakanNilaiYangDiakui(t *testing.T) {
	p := penampung()
	d := NewDebouncer(30*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(1, true)
	p.tunggu(t, 2*time.Second)

	d.Reset()
	if _, tahu := d.Stable(1); tahu {
		t.Fatal("Stable(1) masih diketahui setelah Reset")
	}

	// Nilai yang sama harus diakui ulang, bukan ditelan karena "sama dengan yang lama".
	d.Observe(1, true)
	if ev := p.tunggu(t, 2*time.Second); !ev.High || ev.LoopID != 1 {
		t.Fatalf("event setelah Reset = %+v, want LoopID=1 High=true", ev)
	}
}

func TestDebouncerStopMenghentikanEmit(t *testing.T) {
	p := penampung()
	d := NewDebouncer(50*time.Millisecond, p.emit)

	d.Observe(1, true)
	d.Stop() // sebelum jendela habis

	p.takAdaEvent(t, 200*time.Millisecond)

	// Idempoten, dan Observe setelah Stop tak melakukan apa-apa.
	d.Stop()
	d.Observe(1, false)
	p.takAdaEvent(t, 100*time.Millisecond)
}

func TestDebouncerJendelaBawaan(t *testing.T) {
	d := NewDebouncer(0, nil)
	t.Cleanup(d.Stop)
	if d.window != DefaultDebounce {
		t.Fatalf("window = %v, want %v", d.window, DefaultDebounce)
	}
	if DefaultDebounce < 150*time.Millisecond {
		t.Fatalf("DefaultDebounce = %v, PRD v3 §6.1 mensyaratkan >= 150ms", DefaultDebounce)
	}
}

// ── Integrasi dengan Device ──

func tungguLoopEvent(t *testing.T, d *Device) hw.LoopEvent {
	t.Helper()
	select {
	case ev, ok := <-d.LoopEvents():
		if !ok {
			t.Fatal("kanal LoopEvents tertutup tak terduga")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu LoopEvent dari Device")
		return hw.LoopEvent{}
	}
}

func TestDeviceMemancarkanLoopEventTerDebounce(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), WithDebounce(30*time.Millisecond))
	conn := p.terima(t)

	if _, err := conn.Write(frame("IN2ON")); err != nil {
		t.Fatalf("perangkat gagal menulis: %v", err)
	}

	ev := tungguLoopEvent(t, d)
	if ev.LoopID != 2 || !ev.High {
		t.Fatalf("LoopEvent = %+v, want LoopID=2 High=true", ev)
	}

	// Frame mentahnya tetap terlihat di Frames() sebagai bahan diagnostik.
	if got := tungguFrameDevice(t, d); got != "IN2ON" {
		t.Fatalf("frame mentah = %q, want IN2ON", got)
	}
}

func TestDeviceMenyaringPantulanDariKabel(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), WithDebounce(60*time.Millisecond))
	conn := p.terima(t)

	// Kontak memantul di kabel sebelum akhirnya diam di HIGH.
	for _, f := range []string{"IN1ON", "IN1OFF", "IN1ON", "IN1OFF", "IN1ON"} {
		if _, err := conn.Write(frame(f)); err != nil {
			t.Fatalf("perangkat gagal menulis %s: %v", f, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	ev := tungguLoopEvent(t, d)
	if ev.LoopID != 1 || !ev.High {
		t.Fatalf("LoopEvent = %+v, want LoopID=1 High=true", ev)
	}

	select {
	case lagi := <-d.LoopEvents():
		t.Fatalf("pantulan lolos jadi event kedua: %+v", lagi)
	case <-time.After(200 * time.Millisecond):
	}
}

// Setelah putus-sambung, pembacaan pertama harus diakui ulang — Edge tidak berhak
// menganggap posisi loop masih sama seperti sebelum ia buta.
func TestDeviceMengakuiUlangLoopSetelahReconnect(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), WithDebounce(30*time.Millisecond))

	conn1 := p.terima(t)
	if _, err := conn1.Write(frame("IN2ON")); err != nil {
		t.Fatalf("sesi 1 gagal menulis: %v", err)
	}
	if ev := tungguLoopEvent(t, d); !ev.High {
		t.Fatalf("sesi 1: event = %+v, want High=true", ev)
	}

	if err := conn1.Close(); err != nil {
		t.Fatalf("gagal memutus sesi 1: %v", err)
	}
	conn2 := p.terima(t)

	// Nilai yang sama persis dikirim lagi; tanpa Reset ia akan ditelan.
	if _, err := conn2.Write(frame("IN2ON")); err != nil {
		t.Fatalf("sesi 2 gagal menulis: %v", err)
	}
	if ev := tungguLoopEvent(t, d); ev.LoopID != 2 || !ev.High {
		t.Fatalf("sesi 2: event = %+v, want LoopID=2 High=true", ev)
	}
}

func TestDeviceMenutupKanalLoopEvents(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := NewDevice(p.alamat(), append(opsiDasarUji(), WithDebounce(30*time.Millisecond))...)
	d.Start(context.Background())
	p.terima(t)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	tenggat := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-d.LoopEvents():
			if !ok {
				return
			}
		case <-tenggat:
			t.Fatal("kanal LoopEvents tidak tertutup setelah Close")
		}
	}
}
