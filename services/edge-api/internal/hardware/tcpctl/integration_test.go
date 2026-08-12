// Package tcpctl_test menguji driver A6/A9 dari luar, lewat socket TCP sungguhan,
// terhadap simulator controller (task 1.10).
//
// Paketnya sengaja terpisah dari tcpctl: simdev mengimpor tcpctl, jadi uji internal
// tak dapat memakainya tanpa membentuk daur impor. Berada di luar juga membuat tes ini
// hanya menyentuh API publik — persis yang dilihat pemakai driver.
//
// Seluruh isinya murni Go dan berjalan dalam proses, tanpa perangkat keras, sehingga
// dapat dijalankan di CI.
package tcpctl_test

import (
	"context"
	"errors"
	"testing"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl/simdev"
)

const tenggat = 5 * time.Second

func siapkan(t *testing.T, ubah func(*tcpctl.GateConfig)) (*tcpctl.Gate, *tcpctl.Device, *simdev.Device) {
	t.Helper()

	sim, err := simdev.New("", simdev.WithPulse(50*time.Millisecond))
	if err != nil {
		t.Fatalf("simdev.New: %v", err)
	}
	t.Cleanup(func() { _ = sim.Close() })

	dev := tcpctl.NewDevice(sim.Addr(),
		tcpctl.WithReconnectBackoff(tcpctl.Backoff{Min: time.Millisecond, Max: 10 * time.Millisecond, Factor: 2}),
		tcpctl.WithPingInterval(40*time.Millisecond),
		tcpctl.WithMaxMissedPing(5),
		tcpctl.WithAckTimeout(200*time.Millisecond),
		tcpctl.WithDebounce(30*time.Millisecond),
	)
	ctx, batal := context.WithCancel(context.Background())
	t.Cleanup(batal)
	dev.Start(ctx)
	t.Cleanup(func() { _ = dev.Close() })

	tungguSampai(t, func() bool { return dev.Status() == tcpctl.StatusOnline }, "driver ONLINE")

	cfg := tcpctl.DefaultGateConfig(tcpctl.GateEntry)
	if ubah != nil {
		ubah(&cfg)
	}
	g, err := tcpctl.NewGate(dev, cfg)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	return g, dev, sim
}

func tungguSampai(t *testing.T, cek func() bool, apa string) {
	t.Helper()
	batas := time.Now().Add(tenggat)
	for time.Now().Before(batas) {
		if cek() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout menunggu: %s", apa)
}

// Siklus masuk lengkap di atas socket sungguhan: kendaraan tiba, palang naik,
// kendaraan lewat, palang turun.
func TestIntegrasiSiklusMasuk(t *testing.T) {
	g, _, sim := siapkan(t, nil)
	ctx := context.Background()
	cfg := g.Config()

	pre := g.LoopPre.Subscribe()
	post := g.LoopPost.Subscribe()

	// ① Kendaraan tiba di loop depan.
	if err := sim.SetInput(cfg.Pins.LoopPre, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, pre, true)

	// ② Otorisasi → palang naik, lampu hijau.
	if err := g.Barrier.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay palang menyala")

	if err := g.Light.Set(ctx, hw.PatternGreen); err != nil {
		t.Fatalf("Set hijau: %v", err)
	}
	tungguSampai(t, func() bool {
		return sim.Output(cfg.Pins.GreenLight) && !sim.Output(cfg.Pins.RedLight)
	}, "lampu hijau menyala")

	// ③ Kendaraan masuk ke bawah palang.
	if err := sim.SetInput(cfg.Pins.LoopUnder, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, post, true)

	// ④ Selama masih di bawah palang, penutupan wajib ditolak (P4).
	if err := g.Barrier.Close(ctx); !errors.Is(err, hw.ErrSafetyInterlock) {
		t.Fatalf("Close saat loop bawah HIGH = %v, want ErrSafetyInterlock", err)
	}
	if !sim.Output(cfg.Pins.Barrier) {
		t.Fatal("relay palang padam padahal interlock menolak penutupan")
	}

	// ⑤ Kendaraan lewat → palang boleh turun.
	if err := sim.SetInput(cfg.Pins.LoopUnder, false); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, post, false)

	if err := g.Barrier.Close(ctx); err != nil {
		t.Fatalf("Close setelah kendaraan lewat: %v", err)
	}
	tungguSampai(t, func() bool { return !sim.Output(cfg.Pins.Barrier) }, "relay palang padam")
}

// putusLaluPulih memutus koneksi dan menunggu sampai jalur benar-benar hidup lagi.
//
// Menunggu StatusOnline saja tidak cukup: tepat setelah PutusKoneksi, supervisor belum
// tentu menyadari putusnya, sehingga status lama masih ONLINE dan event yang dikirim
// sesudahnya jatuh ke koneksi yang sudah mati. Karena itu putusnya harus terkonfirmasi
// dulu, lalu koneksi baru harus benar-benar terdaftar di sisi simulator.
func putusLaluPulih(t *testing.T, dev *tcpctl.Device, sim *simdev.Device) {
	t.Helper()
	sebelum := dev.Stats().Disconnects

	sim.PutusKoneksi()
	tungguSampai(t, func() bool { return dev.Stats().Disconnects > sebelum }, "putusnya koneksi tercatat")
	tungguSampai(t, func() bool { return dev.Status() == tcpctl.StatusOnline }, "driver ONLINE lagi")
	tungguSampai(t, func() bool { return sim.Terkoneksi() > 0 }, "simulator menerima koneksi baru")
}

func tungguEvent(t *testing.T, ch <-chan hw.LoopEvent, high bool) {
	t.Helper()
	batas := time.After(tenggat)
	for {
		select {
		case ev := <-ch:
			if ev.High == high {
				return
			}
		case <-batas:
			t.Fatalf("timeout menunggu LoopEvent High=%v", high)
		}
	}
}

// Pantulan kontak di kabel tidak boleh menjadi tepi palsu di state machine.
func TestIntegrasiPantulanTersaring(t *testing.T) {
	g, _, sim := siapkan(t, nil)
	cfg := g.Config()

	post := g.LoopPost.Subscribe()
	if err := sim.Pantul(cfg.Pins.LoopUnder, 4, 3*time.Millisecond, true); err != nil {
		t.Fatalf("Pantul: %v", err)
	}

	tungguEvent(t, post, true)
	select {
	case ev := <-post:
		t.Fatalf("pantulan lolos sebagai event tambahan: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestIntegrasiTapKartu(t *testing.T) {
	g, _, sim := siapkan(t, nil)

	taps := g.RFID.Subscribe()
	sim.Tap("1a2b3c")

	select {
	case tap := <-taps:
		if tap.UID != "1A2B3C" {
			t.Fatalf("UID = %q, want 1A2B3C", tap.UID)
		}
	case <-time.After(tenggat):
		t.Fatal("timeout menunggu tap kartu")
	}
}

func TestIntegrasiBarrierState(t *testing.T) {
	g, _, sim := siapkan(t, nil)
	ctx := context.Background()
	cfg := g.Config()

	st, err := g.Barrier.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if st != hw.BarrierClosed {
		t.Fatalf("State awal = %v, want BarrierClosed", st)
	}

	if err := g.Barrier.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay palang menyala")

	if st, err = g.Barrier.State(ctx); err != nil || st != hw.BarrierOpen {
		t.Fatalf("State = (%v,%v), want (BarrierOpen,nil)", st, err)
	}
}

func TestIntegrasiModePulse(t *testing.T) {
	g, _, sim := siapkan(t, func(cfg *tcpctl.GateConfig) { cfg.BarrierMode = tcpctl.BarrierPulse })
	cfg := g.Config()

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay menyala oleh TRIG")

	// Relay padam sendiri setelah pulsa — Edge tak mengirim apa pun.
	tungguSampai(t, func() bool { return !sim.Output(cfg.Pins.Barrier) }, "relay padam sendiri")
}

// Kabel LAN dicabut / controller reboot: driver harus menyambung sendiri dan
// perintah setelahnya kembali sampai ke perangkat.
func TestIntegrasiPulihSetelahKoneksiPutus(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	cfg := g.Config()

	putusLaluPulih(t, dev, sim)

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open setelah pulih: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay palang menyala setelah pulih")
}

// Controller hang: socket tetap terbuka tetapi berhenti membalas. Driver harus
// menyadarinya lewat PING, memutus paksa, lalu pulih saat controller waras kembali.
func TestIntegrasiPulihSetelahControllerBisu(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	cfg := g.Config()

	sim.Diamkan(true)
	tungguSampai(t, func() bool { return dev.Stats().Unresponsive >= 1 }, "controller ditandai bisu")

	sim.Diamkan(false)
	tungguSampai(t, func() bool { return dev.Status() == tcpctl.StatusOnline }, "driver ONLINE lagi")

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open setelah pulih: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay palang menyala setelah pulih")
}

// Setelah putus-sambung, posisi loop harus diakui ulang — Edge tak berhak menganggap
// keadaan lama masih berlaku setelah sempat buta.
func TestIntegrasiLoopDiakuiUlangSetelahPulih(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	cfg := g.Config()

	post := g.LoopPost.Subscribe()
	if err := sim.SetInput(cfg.Pins.LoopUnder, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, post, true)

	putusLaluPulih(t, dev, sim)

	// Nilai yang sama dikirim ulang; tanpa reset penyaring ia akan ditelan.
	if err := sim.SetInput(cfg.Pins.LoopUnder, false); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if err := sim.SetInput(cfg.Pins.LoopUnder, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, post, true)
}

func TestIntegrasiTelemetryMerekamLaluLintas(t *testing.T) {
	g, dev, _ := siapkan(t, nil)

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	var adaTX, adaRX bool
	for _, e := range dev.Telemetry() {
		if e.Dir == tcpctl.DirTX && e.Payload == "OUT1ON" {
			adaTX = true
		}
		if e.Dir == tcpctl.DirRX && e.Payload == "OUT1ONOK" {
			adaRX = true
		}
	}
	if !adaTX || !adaRX {
		t.Fatalf("telemetry tak merekam perintah & balasan (TX=%v RX=%v)", adaTX, adaRX)
	}
}

func TestIntegrasiSimdevStatMencerminkanKeadaan(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	cfg := g.Config()

	if err := sim.SetInput(cfg.Pins.LoopPre, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay palang menyala")

	jawab, err := dev.Exec(context.Background(), tcpctl.CmdStat)
	if err != nil {
		t.Fatalf("Exec STAT: %v", err)
	}
	inputs, outputs, ok := tcpctl.ParseStat(jawab)
	if !ok {
		t.Fatalf("STAT tak terbaca: %q", jawab)
	}
	if !inputs[cfg.Pins.LoopPre-1] {
		t.Fatalf("STAT tak mencerminkan loop depan HIGH: %q", jawab)
	}
	if !outputs[cfg.Pins.Barrier-1] {
		t.Fatalf("STAT tak mencerminkan relay palang menyala: %q", jawab)
	}
}
