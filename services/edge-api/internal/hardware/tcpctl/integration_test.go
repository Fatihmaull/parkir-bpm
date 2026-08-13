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

// ── Resync STAT setelah pulih (task 3.2) ──

// siapkanResync membangun gerbang seperti siapkan, tetapi opsi Device-nya dapat
// diubah — uji resync perlu mematikan keepalive atau resync itu sendiri.
func siapkanResync(t *testing.T, siapSim func(*simdev.Device), extra ...tcpctl.DeviceOption) (*tcpctl.Gate, *tcpctl.Device, *simdev.Device, <-chan hw.LoopEvent) {
	t.Helper()

	sim, err := simdev.New("", simdev.WithPulse(50*time.Millisecond))
	if err != nil {
		t.Fatalf("simdev.New: %v", err)
	}
	t.Cleanup(func() { _ = sim.Close() })

	// Keadaan lapangan disiapkan SEBELUM driver menyambung, sehingga event apa pun
	// disiarkan ke nol koneksi — persis seperti kendaraan yang tiba saat Edge mati.
	if siapSim != nil {
		siapSim(sim)
	}

	opts := append([]tcpctl.DeviceOption{
		tcpctl.WithReconnectBackoff(tcpctl.Backoff{Min: time.Millisecond, Max: 10 * time.Millisecond, Factor: 2}),
		tcpctl.WithPingInterval(40 * time.Millisecond),
		tcpctl.WithMaxMissedPing(5),
		tcpctl.WithAckTimeout(200 * time.Millisecond),
		tcpctl.WithDebounce(30 * time.Millisecond),
	}, extra...)

	dev := tcpctl.NewDevice(sim.Addr(), opts...)

	// Gerbang dirangkai dan langganan dibuka SEBELUM dev.Start, bukan sesudahnya.
	//
	// Resync startup memancarkan tepi seed-nya sesaat setelah koneksi pertama terbentuk.
	// Berlangganan setelah itu berarti balapan dengan salurkanLoop: kalau ia sempat
	// mengambil event dari loopEvents selagi daftar subscriber masih kosong, event itu
	// hilang untuk selamanya dan uji gagal tanpa ada yang rusak. Merangkai lebih dulu
	// menutup jendela itu sepenuhnya — belum ada koneksi, jadi belum ada yang bisa
	// terlewat.
	g, err := tcpctl.NewGate(dev, tcpctl.DefaultGateConfig(tcpctl.GateEntry))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })
	post := g.LoopPost.Subscribe()

	ctx, batal := context.WithCancel(context.Background())
	t.Cleanup(batal)
	dev.Start(ctx)
	t.Cleanup(func() { _ = dev.Close() })

	tungguSampai(t, func() bool { return dev.Status() == tcpctl.StatusOnline }, "driver ONLINE")

	return g, dev, sim, post
}

// Inti task 3.2: kendaraan yang sudah berdiri di atas loop sejak SEBELUM koneksi putus
// tidak menghasilkan tepi baru saat koneksi pulih. Tanpa resync ia tak terlihat sampai
// kendaraan itu pergi — dan tepi turunnya justru memerintahkan palang menutup.
func TestIntegrasiResyncMenemukanLoopYangSudahHIGH(t *testing.T) {
	g, dev, sim, post := siapkanResync(t, nil)
	cfg := g.Config()

	// Kendaraan tiba selagi koneksi masih sehat.
	if err := sim.SetInput(cfg.Pins.LoopUnder, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, post, true)

	sebelum := dev.Stats().StatResyncs

	// Koneksi putus dan pulih; kendaraan TIDAK bergerak selama itu.
	putusLaluPulih(t, dev, sim)

	tungguSampai(t, func() bool { return dev.Stats().StatResyncs > sebelum }, "resync STAT dijalankan setelah pulih")

	// Resync harus mengumumkan ulang loop yang masih HIGH, tanpa kendaraan bergerak.
	tungguEvent(t, post, true)

	if high, diketahui := dev.LoopState(cfg.Pins.LoopUnder); !diketahui || !high {
		t.Fatalf("LoopState(%d) = (%v,%v), want (true,true)", cfg.Pins.LoopUnder, high, diketahui)
	}
}

// Resync juga berlaku saat startup, bukan hanya reconnect: Edge yang baru dinyalakan
// menghadapi lahan yang sudah terisi.
func TestIntegrasiResyncSaatStartup(t *testing.T) {
	cfg := tcpctl.DefaultGateConfig(tcpctl.GateEntry)

	_, dev, _, post := siapkanResync(t, func(s *simdev.Device) {
		// Kendaraan sudah ada sebelum driver menyambung sama sekali.
		if err := s.SetInput(cfg.Pins.LoopUnder, true); err != nil {
			t.Fatalf("SetInput: %v", err)
		}
	})

	tungguSampai(t, func() bool { return dev.Stats().StatResyncs >= 1 }, "resync STAT dijalankan saat startup")
	tungguEvent(t, post, true)

	if high, diketahui := dev.LoopState(cfg.Pins.LoopUnder); !diketahui || !high {
		t.Fatalf("LoopState = (%v,%v), want (true,true)", high, diketahui)
	}
}

// Kanal LOW tidak boleh diumumkan. Saat lahan sepi keempat kanal LOW, dan
// mengumumkannya berarti memancarkan tepi TURUN palsu pada tiap reconnect — tepi yang
// dipakai state machine untuk menutup palang (§6.2).
func TestIntegrasiResyncTidakMengumumkanKanalLOW(t *testing.T) {
	_, dev, sim, post := siapkanResync(t, nil)

	sebelum := dev.Stats().StatResyncs

	putusLaluPulih(t, dev, sim) // lahan sepi: seluruh input LOW

	tungguSampai(t, func() bool { return dev.Stats().StatResyncs > sebelum }, "resync STAT dijalankan")

	select {
	case ev := <-post:
		t.Fatalf("resync memancarkan event untuk kanal LOW: %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

// Potret STAT bisa tiba setelah event nyata yang lebih baru sudah masuk. Yang lebih
// baru harus menang — resync tak boleh memundurkan status ke masa lalu.
func TestIntegrasiResyncTidakMenimpaEventYangLebihBaru(t *testing.T) {
	cfg := tcpctl.DefaultGateConfig(tcpctl.GateEntry)

	// Controller melaporkan LOW pada potret, tetapi kendaraan tiba tepat setelahnya.
	_, dev, sim, post := siapkanResync(t, nil)

	if err := sim.SetInput(cfg.Pins.LoopUnder, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguEvent(t, post, true)

	tungguSampai(t, func() bool { return dev.Stats().StatResyncs >= 1 }, "resync STAT selesai")

	// Status akhir harus mengikuti event nyata, bukan potret awal yang LOW.
	if high, diketahui := dev.LoopState(cfg.Pins.LoopUnder); !diketahui || !high {
		t.Fatalf("LoopState = (%v,%v), want (true,true) — potret lama menimpa event baru", high, diketahui)
	}
}

// LastStat membuka potret terakhir, termasuk posisi relay — bahan halaman Hardware &
// healthcheck per gerbang (task 3.4).
func TestIntegrasiResyncMerekamPotretTerakhir(t *testing.T) {
	cfg := tcpctl.DefaultGateConfig(tcpctl.GateEntry)

	_, dev, _, _ := siapkanResync(t, func(s *simdev.Device) {
		if err := s.SetInput(cfg.Pins.LoopPre, true); err != nil {
			t.Fatalf("SetInput: %v", err)
		}
	})

	tungguSampai(t, func() bool { return dev.Stats().StatResyncs >= 1 }, "resync STAT selesai")

	potret, ok := dev.LastStat()
	if !ok {
		t.Fatal("LastStat() belum terisi setelah resync berhasil")
	}
	if !potret.Inputs[cfg.Pins.LoopPre-1] {
		t.Fatalf("potret.Inputs = %v, want kanal %d HIGH", potret.Inputs, cfg.Pins.LoopPre)
	}
	if potret.At.IsZero() {
		t.Fatal("potret.At kosong")
	}
}

// Controller yang tak membalas STAT tidak boleh menjatuhkan koneksi atau menggagalkan
// startup (P2). Statusnya cukup dicatat, dan posisi loop kembali dipelajari dari event
// saja — persis perilaku sebelum 3.2.
func TestIntegrasiResyncGagalTidakMerusakKoneksi(t *testing.T) {
	cfg := tcpctl.DefaultGateConfig(tcpctl.GateEntry)

	// Keepalive dimatikan supaya membisukan simulator hanya melumpuhkan STAT, bukan
	// ikut memicu deteksi controller bisu.
	g, dev, sim, _ := siapkanResync(t,
		func(s *simdev.Device) { s.Diamkan(true) },
		tcpctl.WithPingInterval(0),
		tcpctl.WithAckTimeout(50*time.Millisecond),
		tcpctl.WithMaxAttempts(2),
	)

	tungguSampai(t, func() bool { return dev.Stats().StatResyncFailures >= 1 }, "kegagalan resync tercatat")

	if dev.Status() != tcpctl.StatusOnline {
		t.Fatalf("Status = %v, want ONLINE — resync gagal tak boleh menjatuhkan koneksi", dev.Status())
	}
	if _, diketahui := dev.LoopState(cfg.Pins.LoopUnder); diketahui {
		t.Fatal("loop dianggap diketahui padahal resync gagal")
	}

	// Controller waras kembali: gerbang tetap dapat dipakai.
	sim.Diamkan(false)
	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open setelah resync gagal: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(cfg.Pins.Barrier) }, "relay palang menyala")
}

// Katup darurat WithStatResync(false) benar-benar mematikan resync.
func TestIntegrasiResyncDapatDimatikan(t *testing.T) {
	cfg := tcpctl.DefaultGateConfig(tcpctl.GateEntry)

	_, dev, _, _ := siapkanResync(t,
		func(s *simdev.Device) {
			if err := s.SetInput(cfg.Pins.LoopUnder, true); err != nil {
				t.Fatalf("SetInput: %v", err)
			}
		},
		tcpctl.WithStatResync(false),
	)

	time.Sleep(200 * time.Millisecond)

	if n := dev.Stats().StatResyncs; n != 0 {
		t.Fatalf("StatResyncs = %d, want 0 saat resync dimatikan", n)
	}
	if _, diketahui := dev.LoopState(cfg.Pins.LoopUnder); diketahui {
		t.Fatal("loop diketahui padahal resync dimatikan dan tak ada event")
	}
}
