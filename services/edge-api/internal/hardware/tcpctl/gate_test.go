package tcpctl

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// siapkanGate merangkai Gate di atas Device yang tersambung ke controller palsu.
func siapkanGate(t *testing.T, ubah func(*GateConfig)) (*Gate, *controllerPalsu, net.Conn) {
	t.Helper()

	cfg := DefaultGateConfig(GateEntry)
	if ubah != nil {
		ubah(&cfg)
	}

	p := mulaiPerangkatPalsu(t)
	dev := mulaiDevice(t, p.alamat(),
		WithAckTimeout(80*time.Millisecond),
		WithMaxAttempts(3),
		WithDebounce(20*time.Millisecond),
	)
	conn := p.terima(t)
	c := mulaiController(conn)
	tungguSampai(t, 3*time.Second, dev.Connected, "Device tersambung")

	g, err := NewGate(dev, cfg)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g, c, conn
}

// tungguPerintah menunggu satu perintah tertentu sampai ke controller palsu.
func tungguPerintah(t *testing.T, c *controllerPalsu, want string) {
	t.Helper()
	tenggat := time.After(3 * time.Second)
	for {
		select {
		case cmd := <-c.diterima:
			if cmd == want {
				return
			}
		case <-tenggat:
			t.Fatalf("timeout menunggu perintah %q", want)
		}
	}
}

// naikkanLoop menggerakkan satu kanal input dan menunggu nilainya diakui penyaring.
func naikkanLoop(t *testing.T, dev *Device, conn net.Conn, ch int, high bool) {
	t.Helper()
	akhiran := "ON"
	if !high {
		akhiran = "OFF"
	}
	f := "IN" + string(rune('0'+ch)) + akhiran
	if _, err := conn.Write(frame(f)); err != nil {
		t.Fatalf("perangkat gagal menulis %s: %v", f, err)
	}
	tungguSampai(t, 3*time.Second, func() bool {
		got, diketahui := dev.LoopState(ch)
		return diketahui && got == high
	}, "loop kanal "+f+" diakui")
}

func TestGateBarrierOpenModeHold(t *testing.T) {
	g, c, _ := siapkanGate(t, nil)

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguPerintah(t, c, "OUT1ON")
}

func TestGateBarrierOpenModePulse(t *testing.T) {
	g, c, _ := siapkanGate(t, func(cfg *GateConfig) { cfg.BarrierMode = BarrierPulse })

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguPerintah(t, c, "TRIG1")
}

func TestGateBarrierCloseSaatLoopBawahLOW(t *testing.T) {
	g, c, _ := siapkanGate(t, nil)

	if err := g.Barrier.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	tungguPerintah(t, c, "OUT1OFF")
}

// Inti P4 / task 2.3: palang menolak menutup selama loop di bawahnya HIGH, di jalur
// driver nyata — bukan cuma di simulator.
func TestGateBarrierTolakTutupSaatLoopBawahHIGH(t *testing.T) {
	g, c, conn := siapkanGate(t, nil)
	dev := g.dev

	naikkanLoop(t, dev, conn, g.cfg.Pins.LoopUnder, true)

	err := g.Barrier.Close(context.Background())
	if !errors.Is(err, hw.ErrSafetyInterlock) {
		t.Fatalf("Close saat loop bawah HIGH = %v, want ErrSafetyInterlock", err)
	}

	// Perintah menutup tidak boleh sampai ke kabel sama sekali.
	select {
	case cmd := <-c.diterima:
		if cmd == "OUT1OFF" {
			t.Fatal("OUT1OFF terkirim padahal interlock aktif")
		}
	case <-time.After(200 * time.Millisecond):
	}

	// Setelah kendaraan lewat, penutupan boleh dilakukan.
	naikkanLoop(t, dev, conn, g.cfg.Pins.LoopUnder, false)
	if err := g.Barrier.Close(context.Background()); err != nil {
		t.Fatalf("Close setelah loop turun: %v", err)
	}
	tungguPerintah(t, c, "OUT1OFF")
}

// Interlock harus tetap berlaku di tengah rentetan retry: loop yang naik setelah
// percobaan pertama wajib menghentikan percobaan berikutnya.
func TestGateInterlockBerlakuSaatRetry(t *testing.T) {
	g, c, conn := siapkanGate(t, nil)
	dev := g.dev

	c.abaikan.Store(99) // controller bisu → Close akan mengulang

	hasil := make(chan error, 1)
	go func() { hasil <- g.Barrier.Close(context.Background()) }()

	// Percobaan pertama sudah terkirim; sekarang kendaraan masuk ke bawah palang.
	tungguPerintah(t, c, "OUT1OFF")
	naikkanLoop(t, dev, conn, g.cfg.Pins.LoopUnder, true)

	select {
	case err := <-hasil:
		if !errors.Is(err, hw.ErrSafetyInterlock) {
			t.Fatalf("Close = %v, want terbungkus ErrSafetyInterlock", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout menunggu Close menyerah")
	}
}

// Mode pulse: Edge tak punya perintah menutup, jadi Close tak mengirim apa pun.
func TestGateBarrierCloseModePulseTanpaOperasi(t *testing.T) {
	g, c, _ := siapkanGate(t, func(cfg *GateConfig) { cfg.BarrierMode = BarrierPulse })

	if err := g.Barrier.Close(context.Background()); err != nil {
		t.Fatalf("Close mode pulse = %v, want nil", err)
	}
	select {
	case cmd := <-c.diterima:
		t.Fatalf("mode pulse mengirim %q, seharusnya tak mengirim apa pun", cmd)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestGateBarrierState(t *testing.T) {
	g, c, _ := siapkanGate(t, nil)

	// Output1 = '1' → palang terbuka.
	c.setStat("STAT00001000")
	got, err := g.Barrier.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if got != hw.BarrierOpen {
		t.Fatalf("State = %v, want BarrierOpen", got)
	}

	c.setStat("STAT00000000")
	if got, err = g.Barrier.State(context.Background()); err != nil || got != hw.BarrierClosed {
		t.Fatalf("State = (%v,%v), want (BarrierClosed,nil)", got, err)
	}

	// Balasan yang tak sesuai bentuk tidak boleh ditebak jadi posisi palang (P3).
	c.setStat("STATxx")
	if got, err = g.Barrier.State(context.Background()); err == nil || got != hw.BarrierUnknown {
		t.Fatalf("State pada balasan rusak = (%v,%v), want (Unknown, error)", got, err)
	}
}

func TestGateBarrierStateModePulseTakDiketahui(t *testing.T) {
	g, _, _ := siapkanGate(t, func(cfg *GateConfig) { cfg.BarrierMode = BarrierPulse })

	got, err := g.Barrier.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if got != hw.BarrierUnknown {
		t.Fatalf("State mode pulse = %v, want BarrierUnknown", got)
	}
}

func TestGateLoopSubscribeTerpisahPerKanal(t *testing.T) {
	g, _, conn := siapkanGate(t, nil)

	pre := g.LoopPre.Subscribe()
	post := g.LoopPost.Subscribe()

	if _, err := conn.Write(frame("IN2ON")); err != nil { // LoopUnder
		t.Fatalf("gagal menulis: %v", err)
	}

	select {
	case ev := <-post:
		if ev.LoopID != g.cfg.Pins.LoopUnder || !ev.High {
			t.Fatalf("event = %+v, want kanal %d High", ev, g.cfg.Pins.LoopUnder)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu event di LoopPost")
	}

	select {
	case ev := <-pre:
		t.Fatalf("LoopPre menerima event kanal lain: %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestGateLoopReadPakaiNilaiTerDebounce(t *testing.T) {
	g, _, conn := siapkanGate(t, nil)

	naikkanLoop(t, g.dev, conn, g.cfg.Pins.LoopUnder, true)
	high, err := g.LoopPost.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !high {
		t.Fatal("Read = false, want true")
	}
}

// Sebelum ada pembacaan stabil, Read jatuh ke STAT.
func TestGateLoopReadJatuhKeStat(t *testing.T) {
	g, c, _ := siapkanGate(t, nil)

	c.setStat("STAT01000000") // Input2 = '1'
	high, err := g.LoopPost.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !high {
		t.Fatal("Read = false, want true dari STAT")
	}
}

func TestGateLightPola(t *testing.T) {
	g, c, _ := siapkanGate(t, nil)
	ctx := context.Background()

	if err := g.Light.Set(ctx, hw.PatternGreen); err != nil {
		t.Fatalf("Set hijau: %v", err)
	}
	tungguPerintah(t, c, "OUT2ON")
	tungguPerintah(t, c, "OUT3OFF")

	if err := g.Light.Set(ctx, hw.PatternRed); err != nil {
		t.Fatalf("Set merah: %v", err)
	}
	tungguPerintah(t, c, "OUT2OFF")
	tungguPerintah(t, c, "OUT3ON")

	if err := g.Light.Set(ctx, hw.PatternOff); err != nil {
		t.Fatalf("Set mati: %v", err)
	}
	tungguPerintah(t, c, "OUT3OFF")
}

func TestGateLightRedBlinkBerkedip(t *testing.T) {
	g, c, _ := siapkanGate(t, nil)

	if err := g.Light.Set(context.Background(), hw.PatternRedBlink); err != nil {
		t.Fatalf("Set redblink: %v", err)
	}
	// Nyala dulu, lalu padam pada tick berikutnya.
	tungguPerintah(t, c, "OUT3ON")
	tungguPerintah(t, c, "OUT3OFF")
	tungguPerintah(t, c, "OUT3ON")

	// Pola berikutnya harus menghentikan pengedip.
	if err := g.Light.Set(context.Background(), hw.PatternGreen); err != nil {
		t.Fatalf("Set hijau: %v", err)
	}
}

// Peta pin v3 tak punya kanal kuning; menyalakan warna lain diam-diam akan memberi
// pengemudi isyarat yang keliru.
func TestGateLightYellowBlinkDitolak(t *testing.T) {
	g, _, _ := siapkanGate(t, nil)

	if err := g.Light.Set(context.Background(), hw.PatternYellowBlink); err == nil {
		t.Fatal("PatternYellowBlink seharusnya ditolak")
	}
}

func TestGateRFIDSubscribe(t *testing.T) {
	g, _, conn := siapkanGate(t, nil)

	taps := g.RFID.Subscribe()
	if _, err := conn.Write(frame("W1A2B3C")); err != nil {
		t.Fatalf("gagal menulis: %v", err)
	}

	select {
	case tap := <-taps:
		if tap.UID != "1A2B3C" {
			t.Fatalf("UID = %q, want 1A2B3C", tap.UID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu tap")
	}
}

func TestNewGateMenolakMasukanTakSah(t *testing.T) {
	if _, err := NewGate(nil, DefaultGateConfig(GateEntry)); err == nil {
		t.Fatal("Device nil seharusnya ditolak")
	}

	dev := NewDevice(alamatMati(t), opsiDasarUji()...)
	t.Cleanup(func() { _ = dev.Close() })

	rusak := DefaultGateConfig(GateEntry)
	rusak.Pins.GreenLight = rusak.Pins.Barrier // bentrok relay
	if _, err := NewGate(dev, rusak); err == nil {
		t.Fatal("konfigurasi bentrok seharusnya ditolak")
	}
}

func TestGateCloseIdempoten(t *testing.T) {
	g, _, _ := siapkanGate(t, nil)
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close kedua: %v", err)
	}
}

func TestParseStat(t *testing.T) {
	inputs, outputs, ok := ParseStat("STAT10010110")
	if !ok {
		t.Fatal("ParseStat ok=false, want true")
	}
	if inputs != [ChannelMax]bool{true, false, false, true} {
		t.Fatalf("inputs = %v", inputs)
	}
	if outputs != [ChannelMax]bool{false, true, true, false} {
		t.Fatalf("outputs = %v", outputs)
	}

	takSah := []string{
		"", "STAT", "STAT1001011", "STAT100101100", // panjang salah
		"STAT1001011x", // bukan biner
		"STAT12345678", // digit selain 0/1
		"PINGOK", "OUT1ONOK", "IN1ON",
	}
	for _, f := range takSah {
		if _, _, ok := ParseStat(f); ok {
			t.Fatalf("ParseStat(%q) ok=true, want false", f)
		}
	}
}
