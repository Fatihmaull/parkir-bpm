package tcpctl_test

import (
	"context"
	"testing"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl/simdev"
)

// Rekonsiliasi keadaan setelah blip koneksi (task 3.3).
//
// Seluruh uji di sini memakai simulator controller (simdev), sehingga yang diperiksa
// adalah relay yang benar-benar berubah di sisi perangkat — bukan sekadar pencacah.

// Palang yang dikehendaki TERBUKA harus kembali terbuka setelah controller reboot.
//
// Reboot controller diperagakan dengan menutup relay dari sisi simulator: state machine
// tak pernah memerintahkan tutup, jadi perbedaan itu murni akibat perangkat kehilangan
// keadaannya.
func TestRekonsiliasiMembukaUlangPalang(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	pin := g.Config().Pins.Barrier

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(pin) }, "palang terbuka")

	// Controller kehilangan keadaan relay-nya.
	sim.SetOutput(pin, false)
	putusLaluPulih(t, dev, sim)

	tungguSampai(t, func() bool { return sim.Output(pin) }, "palang terbuka lagi setelah reconnect")

	// Pencacah ditunggu, bukan dibaca sekali: relay sudah berubah di tengah Exec
	// (simulator menyetelnya saat menangani perintah) sedangkan pencatatannya baru
	// terjadi setelah Exec kembali. Membacanya seketika berarti menguji dua momen
	// berbeda dan sesekali gagal tanpa ada yang rusak.
	tungguSampai(t, func() bool { return dev.Stats().Reconciles > 0 }, "rekonsiliasi tercatat")
}

// Niat "tutup" yang DITOLAK interlock tetap tercatat, dan ditegaskan ulang setelah
// koneksi pulih begitu loop bawah terbukti LOW.
//
// Ini inti task 3.3: perintahnya tidak diantre — yang bertahan adalah kehendaknya, dan
// syarat keselamatannya diperiksa ulang pada detik penegasan.
func TestRekonsiliasiMenutupSetelahInterlockLepas(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	pin := g.Config().Pins.Barrier
	chBawah := g.Config().Pins.LoopUnder

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(pin) }, "palang terbuka")

	// Kendaraan masuk ke bawah palang → interlock menolak penutupan.
	if err := sim.SetInput(chBawah, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguSampai(t, func() bool {
		high, diketahui := dev.LoopState(chBawah)
		return diketahui && high
	}, "loop bawah HIGH terbaca")

	if err := g.Barrier.Close(context.Background()); err == nil {
		t.Fatal("Close harus ditolak interlock selama loop bawah HIGH")
	}
	if !sim.Output(pin) {
		t.Fatal("palang tak boleh tertutup saat interlock menolak")
	}

	// Kendaraan pergi, lalu koneksi putus-sambung.
	if err := sim.SetInput(chBawah, false); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguSampai(t, func() bool {
		high, diketahui := dev.LoopState(chBawah)
		return diketahui && !high
	}, "loop bawah LOW terbaca")

	putusLaluPulih(t, dev, sim)

	tungguSampai(t, func() bool { return !sim.Output(pin) },
		"palang tertutup lewat rekonsiliasi setelah interlock lepas")
}

// Penutupan TIDAK direkonsiliasi selama loop bawah masih HIGH — kendaraan masih di
// bawah palang. Ini jantung P4 pada jalur baru ini.
func TestRekonsiliasiTakMenutupSaatLoopBawahHIGH(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	pin := g.Config().Pins.Barrier
	chBawah := g.Config().Pins.LoopUnder

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(pin) }, "palang terbuka")

	if err := sim.SetInput(chBawah, true); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	tungguSampai(t, func() bool {
		high, diketahui := dev.LoopState(chBawah)
		return diketahui && high
	}, "loop bawah HIGH terbaca")

	// Kehendak "tutup" tercatat walau ditolak.
	_ = g.Barrier.Close(context.Background())

	// Kendaraan MASIH di bawah palang saat koneksi pulih.
	dilewatiSebelum := dev.Stats().ReconcileSkipped
	putusLaluPulih(t, dev, sim)

	tungguSampai(t, func() bool { return dev.Stats().ReconcileSkipped > dilewatiSebelum },
		"rekonsiliasi tutup dilewati & tercatat")

	// Beri waktu; palang harus TETAP terbuka.
	time.Sleep(200 * time.Millisecond)
	if !sim.Output(pin) {
		t.Fatal("palang menutup di atas kendaraan yang masih di bawahnya — pelanggaran P4")
	}
}

// Niat "buka" yang sudah basi tidak ditegaskan ulang: kendaraan yang diotorisasi sudah
// lama pergi, dan palang yang terbuka sendiri melayani kendaraan berikutnya yang belum
// membayar.
func TestRekonsiliasiTakMembukaNiatBasi(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	pin := g.Config().Pins.Barrier

	g.SetMaxOpenIntentAge(20 * time.Millisecond)

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(pin) }, "palang terbuka")

	time.Sleep(60 * time.Millisecond) // niat menjadi basi
	sim.SetOutput(pin, false)         // controller kehilangan keadaannya

	dilewatiSebelum := dev.Stats().ReconcileSkipped
	putusLaluPulih(t, dev, sim)

	tungguSampai(t, func() bool { return dev.Stats().ReconcileSkipped > dilewatiSebelum },
		"rekonsiliasi buka dilewati karena niat basi")

	time.Sleep(200 * time.Millisecond)
	if sim.Output(pin) {
		t.Fatal("palang dibuka atas niat basi — kendaraan berikutnya lewat gratis")
	}
}

// Mode pulse tak punya keadaan palang yang bisa direkonsiliasi: "buka" adalah kejadian
// sesaat, dan Edge tak punya perintah menutup sama sekali.
func TestRekonsiliasiTakBerlakuPadaModePulse(t *testing.T) {
	g, dev, sim := siapkan(t, func(c *tcpctl.GateConfig) { c.BarrierMode = tcpctl.BarrierPulse })
	pin := g.Config().Pins.Barrier

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return !sim.Output(pin) }, "pulsa selesai, relay padam sendiri")

	sebelum := dev.Stats().Reconciles
	putusLaluPulih(t, dev, sim)
	time.Sleep(200 * time.Millisecond)

	if sim.Output(pin) {
		t.Fatal("mode pulse tak boleh menegaskan ulang 'buka' — itu kejadian sesaat, bukan keadaan")
	}
	if dev.Stats().Reconciles != sebelum {
		t.Fatalf("palang mode pulse ikut direkonsiliasi: %+v", dev.Stats())
	}
}

// Pola lampu statis ditegaskan ulang setelah reconnect.
func TestRekonsiliasiLampuStatis(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	hijau := g.Config().Pins.GreenLight

	if err := g.Light.Set(context.Background(), hw.PatternGreen); err != nil {
		t.Fatalf("Set: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(hijau) }, "lampu hijau menyala")

	sim.SetOutput(hijau, false)
	putusLaluPulih(t, dev, sim)

	tungguSampai(t, func() bool { return sim.Output(hijau) }, "lampu hijau menyala lagi setelah reconnect")
}

// Gerbang yang belum pernah diperintah apa pun tak menegaskan apa pun. Rekonsiliasi
// yang memaksakan keadaan bawaan akan menggerakkan palang tanpa ada yang memintanya.
func TestRekonsiliasiDiamTanpaNiat(t *testing.T) {
	g, dev, sim := siapkan(t, nil)
	pin := g.Config().Pins.Barrier

	sebelum := dev.Stats().Reconciles
	putusLaluPulih(t, dev, sim)
	time.Sleep(200 * time.Millisecond)

	if dev.Stats().Reconciles != sebelum {
		t.Fatalf("rekonsiliasi berjalan tanpa niat apa pun: %+v", dev.Stats())
	}
	if sim.Output(pin) {
		t.Fatal("palang bergerak tanpa ada yang memerintahkan")
	}
	_ = g
}

// Penutupan TIDAK direkonsiliasi saat status loop bawah BELUM DIKETAHUI.
//
// Inilah yang membedakan rekonsiliasi dari jalur hidup, dan satu-satunya uji yang
// membuktikan perbedaannya. Jalur hidup (periksaInterlock) sengaja MEMBOLEHKAN penutupan
// saat status tak diketahui — memblokir tanpa dasar membuat palang menggantung terbuka
// tiap koneksi pulih. Tapi jalur hidup punya bukti yang tak dimiliki rekonsiliator: ia
// baru saja melihat loop bawah turun. Rekonsiliator tak melihat apa pun, jadi menutup
// atas dasar itu berarti menutup buta.
//
// Skenarionya nyata: controller yang tak mematuhi semantik STAT (H2) tak pernah mengisi
// status loop setelah reconnect. Diperagakan dengan mematikan resync.
func TestRekonsiliasiTakMenutupSaatLoopBawahTakDiketahui(t *testing.T) {
	sim, err := simdev.New("", simdev.WithPulse(50*time.Millisecond))
	if err != nil {
		t.Fatalf("simdev.New: %v", err)
	}
	t.Cleanup(func() { _ = sim.Close() })

	// Backoff sengaja lambat: perintah tutup harus jatuh SAAT terputus supaya ia tak
	// terkirim langsung. Kalau ia sempat terkirim hidup-hidup, palang menutup lewat
	// jalur biasa dan uji ini tak menguji rekonsiliasi sama sekali.
	dev := tcpctl.NewDevice(sim.Addr(),
		tcpctl.WithReconnectBackoff(tcpctl.Backoff{Min: 400 * time.Millisecond, Max: 600 * time.Millisecond, Factor: 1}),
		tcpctl.WithPingInterval(40*time.Millisecond),
		tcpctl.WithMaxMissedPing(5),
		tcpctl.WithAckTimeout(200*time.Millisecond),
		tcpctl.WithDebounce(30*time.Millisecond),
		tcpctl.WithStatResync(false), // controller tak patuh STAT (H2)
	)
	ctx, batal := context.WithCancel(context.Background())
	t.Cleanup(batal)
	dev.Start(ctx)
	t.Cleanup(func() { _ = dev.Close() })
	tungguSampai(t, func() bool { return dev.Status() == tcpctl.StatusOnline }, "driver ONLINE")

	g, err := tcpctl.NewGate(dev, tcpctl.DefaultGateConfig(tcpctl.GateEntry))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	pin := g.Config().Pins.Barrier
	chBawah := g.Config().Pins.LoopUnder

	if err := g.Barrier.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	tungguSampai(t, func() bool { return sim.Output(pin) }, "palang terbuka")

	// Perintah tutup jatuh SAAT terputus: ditolak ErrNotConnected, tapi kehendaknya
	// tercatat. Inilah blip yang jadi alasan task 3.3 ada.
	dilewatiSebelum := dev.Stats().ReconcileSkipped
	sebelumPutus := dev.Stats().Disconnects
	sim.PutusKoneksi()
	tungguSampai(t, func() bool { return dev.Stats().Disconnects > sebelumPutus }, "koneksi terputus")

	if err := g.Barrier.Close(context.Background()); err == nil {
		t.Fatal("Close saat terputus harus gagal — perintah tak boleh diantre diam-diam")
	}
	if !sim.Output(pin) {
		t.Fatal("palang tertutup padahal perintahnya tak pernah terkirim")
	}

	tungguSampai(t, func() bool { return dev.Status() == tcpctl.StatusOnline }, "driver ONLINE lagi")
	tungguSampai(t, func() bool { return sim.Terkoneksi() > 0 }, "simulator menerima koneksi baru")

	tungguSampai(t, func() bool { return dev.Stats().ReconcileSkipped > dilewatiSebelum },
		"rekonsiliasi tutup dilewati karena bukti kurang")

	// Palang HARUS tetap terbuka. Ini harga yang kita terima secara sadar: kerugian
	// pendapatan, ditukar dengan tidak pernah menutup di atas kendaraan yang tak terlihat.
	time.Sleep(200 * time.Millisecond)
	if !sim.Output(pin) {
		t.Fatal("palang menutup tanpa bukti loop bawah LOW — menutup buta, pelanggaran P4")
	}
	if high, diketahui := dev.LoopState(chBawah); diketahui {
		t.Fatalf("prasyarat uji gugur: status loop justru diketahui (high=%v)", high)
	}
}
