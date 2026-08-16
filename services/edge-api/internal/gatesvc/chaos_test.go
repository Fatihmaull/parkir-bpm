package gatesvc

import (
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl/simdev"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// Chaos test tingkat lahan (task 3.6, PRD v3 §19).
//
// Yang diuji di sini bukan "apakah fungsinya benar" — itu tugas uji unit — melainkan
// **apakah lahan tetap melayani** saat sesuatu di lapangan rusak. P8 menuntut kegagalan
// satu bagian tidak menghentikan bagian lain, dan itu hanya terbukti kalau kerusakannya
// benar-benar diperagakan, bukan diandaikan.
//
// Empat kerusakan yang disebut task 3.6: LAN controller dicabut, Edge mati, kertas habis,
// internet putus. "Edge mati" diuji di lapisan driver (tcpctl/rekonsiliasi_test.go,
// task 3.5) karena di sanalah relay yang bertahan melewati matinya proses dapat diamati.

// lahanCampuran menyiapkan lahan dengan satu gerbang NYATA (di atas simulator controller)
// dan dua gerbang tersimulasi — bentuk yang membuat "kegagalan satu gerbang" dapat
// dibedakan dari "kegagalan lahan".
func lahanCampuran(t *testing.T) (*Service, *simdev.Device, *memstore.Store) {
	t.Helper()

	sd, err := simdev.New("", simdev.WithPulse(30*time.Millisecond))
	if err != nil {
		t.Fatalf("simdev.New: %v", err)
	}
	t.Cleanup(func() { _ = sd.Close() })

	hub := wsbus.NewHub()
	store := memstore.New("node-chaos", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	svc, err := New(Config{
		NodeID: "node-chaos", TenantID: "t1", SiteID: "s1",
		Site: gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source: StaticSource{
			{Code: "GATE-IN-01", Kind: KindEntry, Transport: TransportTCP, Endpoint: sd.Addr()},
			{Code: "GATE-IN-02", Kind: KindEntry, Transport: TransportSim},
			{Code: "GATE-OUT-01", Kind: KindExit, Transport: TransportSim},
		},
		HealthInterval:     30 * time.Millisecond,
		HealthProbeTimeout: 500 * time.Millisecond,
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc, sd, store
}

// gerbangMelayani membuktikan satu gerbang tersimulasi masih menjalankan siklusnya penuh.
//
// Hanya berlaku untuk gerbang tersimulasi: gerbang nyata menolak injeksi event, dan itu
// memang benar — event perangkat datang dari lapangan, bukan dari uji (P3).
func gerbangMelayani(t *testing.T, svc *Service, code string) {
	t.Helper()
	r, ok := svc.Gate(code)
	if !ok {
		t.Fatalf("gerbang %s tak ditemukan", code)
	}
	if r.Nyata() {
		t.Fatalf("gerbangMelayani dipakai pada gerbang nyata %s — pakai simdev untuk itu", code)
	}
	if err := r.DriveLoop("pre", true); err != nil {
		t.Fatalf("%s: DriveLoop: %v", code, err)
	}
	if r.Spec().Kind == KindEntry {
		if err := r.FireEntry(gate.Event{Kind: gate.EvButton}); err != nil {
			t.Fatalf("%s: tombol tiket: %v", code, err)
		}
		if st := r.State(); st == string(gate.StateIdle) {
			t.Fatalf("%s tak bergerak dari IDLE — gerbang tak melayani", code)
		}
		return
	}
	// Gerbang keluar: LD3 saja sudah harus memindahkannya ke IDENTIFYING.
	if st := r.State(); st == string(gate.XIdle) {
		t.Fatalf("%s tak bergerak dari IDLE — gerbang tak melayani", code)
	}
}

// ── Kerusakan 1: LAN controller dicabut ──

// Controller satu gerbang dicabut TIDAK boleh menghentikan gerbang lain (P8).
//
// Ini inti P8. Lahan dengan tiga gerbang yang satu kabelnya tercabut harus tetap melayani
// dua sisanya; kalau tidak, satu kabel longgar menghentikan seluruh pendapatan lahan.
func TestChaosCabutLANSatuGerbang(t *testing.T) {
	svc, sd, _ := lahanCampuran(t)
	r1, _ := svc.Gate("GATE-IN-01")

	tungguHingga(t, func() bool {
		return r1.StatusPerangkat() == tcpctl.StatusOnline.String()
	}, "controller gerbang nyata tersambung")

	// Kabel dicabut, dan TETAP tercabut.
	sd.PutusKoneksi()
	_ = sd.Close()

	tungguHingga(t, func() bool {
		return r1.StatusPerangkat() != tcpctl.StatusOnline.String()
	}, "gerbang nyata terbaca terputus")

	// Gerbang yang rusak jujur melaporkan dirinya down...
	if h := r1.Health(time.Second); h.Status != HealthDown {
		t.Fatalf("gerbang tercabut = %q, want %q", h.Status, HealthDown)
	}
	// ...tapi goroutine pemiliknya tetap hidup: yang mati controllernya, bukan gerbangnya.
	if h := r1.Health(time.Second); !h.Menjawab {
		t.Fatal("goroutine pemilik ikut mati saat controller tercabut")
	}

	// Gerbang lain melayani penuh.
	gerbangMelayani(t, svc, "GATE-IN-02")
	if h, _ := svc.Gate("GATE-OUT-01"); h.Health(time.Second).Status != HealthOK {
		t.Fatal("gerbang keluar ikut terseret kegagalan gerbang lain")
	}

	// Rollup lahan memburuk — operator harus tahu — tanpa mesin internal ikut dinyatakan
	// beku, sebab restart edge-api tak akan menyambungkan kabel (K33).
	if kes := svc.Health(time.Second); kes.Status != HealthDown {
		t.Fatalf("rollup lahan = %q, want %q", kes.Status, HealthDown)
	}
	tungguHingga(t, func() bool { return !svc.LastHealthSweep().IsZero() }, "healthcheck menyapu")
	if !svc.MesinInternalHidup() {
		t.Fatal("controller tercabut membekukan penilaian mesin internal — proses akan direstart sia-sia")
	}
}

// ── Kerusakan 2: kertas habis ──

// Kertas habis: casual berhenti, MEMBER TETAP MASUK (D3, §5.4.3).
//
// Memutus akses penghuni karena printer adalah kegagalan yang tak perlu — mereka tak
// butuh tiket, kartunya sendiri yang jadi bukti.
func TestChaosKertasHabis(t *testing.T) {
	svc, _, store := lahanCampuran(t)
	store.AddMember("04A1B2C3", []string{"D1234ABC"}, "mobil", time.Now().AddDate(1, 0, 0))

	r, _ := svc.Gate("GATE-IN-02") // gerbang tersimulasi penuh: printernya dapat dirusak
	sim := r.Sim()
	if sim == nil {
		t.Fatal("prasyarat gugur: gerbang ini seharusnya tersimulasi penuh")
	}
	sim.Printer.SetStatus(hw.PrinterPaperOut)

	// Casual: kendaraan datang, tekan tombol → terkunci, tak ada tiket.
	if err := r.DriveLoop("pre", true); err != nil {
		t.Fatalf("DriveLoop: %v", err)
	}
	if err := r.FireEntry(gate.Event{Kind: gate.EvButton}); err != nil {
		t.Fatalf("tombol tiket: %v", err)
	}
	if st := r.State(); st != string(gate.StateLockedNoPaper) {
		t.Fatalf("state casual = %q, want %q", st, gate.StateLockedNoPaper)
	}

	// Member: tap kartu pada gerbang yang SAMA, dalam keadaan kertas yang sama habisnya.
	if err := r.TapRFID("04A1B2C3"); err != nil {
		t.Fatalf("tap member: %v", err)
	}
	if st := r.State(); st == string(gate.StateLockedNoPaper) {
		t.Fatal("member ikut terkunci saat kertas habis — melanggar D3")
	}

	// Gerbang lain tak terpengaruh sama sekali oleh kertas yang habis di sini.
	gerbangMelayani(t, svc, "GATE-OUT-01")

	r1, _ := svc.Gate("GATE-IN-01")
	tungguHingga(t, func() bool {
		return r1.StatusPerangkat() == tcpctl.StatusOnline.String()
	}, "gerbang nyata tersambung")
	if h := r1.Health(time.Second); h.Status == HealthDown {
		t.Fatalf("gerbang nyata ikut down karena kertas habis di gerbang lain: %v", h.Alasan)
	}
}

// ── Kerusakan 3: internet putus ──

// Internet putus tak boleh menyentuh gerbang sama sekali (P1).
//
// Cloud adalah tujuan replikasi, BUKAN dependensi runtime. Lahan yang berhenti melayani
// saat internet putus berarti offline-first hanya ada di dokumen.
func TestChaosInternetPutus(t *testing.T) {
	// Tanpa SyncEndpoint sama sekali = persis keadaan saat internet putus dari sudut
	// pandang gerbang: tak ada yang bisa dikirim, dan tak ada yang menunggu jawaban.
	svc, _, store := lahanCampuran(t)

	// Tunggu gerbang nyata tersambung dulu. Kalau tidak, yang terukur adalah controller
	// yang memang belum sempat menyambung — bukan akibat internet putus, dan uji ini
	// akan "gagal" karena sebab yang sama sekali lain.
	r1, _ := svc.Gate("GATE-IN-01")
	tungguHingga(t, func() bool {
		return r1.StatusPerangkat() == tcpctl.StatusOnline.String()
	}, "controller gerbang nyata tersambung")

	sebelum := store.Outbox().PendingCount()

	// Seluruh gerbang tetap menjalankan siklusnya.
	gerbangMelayani(t, svc, "GATE-IN-02")

	rOut, _ := svc.Gate("GATE-OUT-01")
	if err := rOut.DriveLoop("pre", true); err != nil {
		t.Fatalf("gerbang keluar: DriveLoop: %v", err)
	}
	if st := rOut.State(); st == string(gate.XIdle) {
		t.Fatal("gerbang keluar tak bergerak saat offline — melanggar P1")
	}

	// Kesehatan lahan tidak memburuk karena Cloud tak terjangkau: sync bukan gerbang.
	// (degraded masih wajar — printer gerbang nyata memang tersimulasi, H1.)
	if kes := svc.Health(time.Second); kes.Status == HealthDown {
		t.Fatalf("lahan dinyatakan down padahal hanya Cloud yang tak terjangkau: %+v", kes.Gates)
	}

	// Yang seharusnya terjadi: pekerjaan menumpuk di outbox, bukan hilang (D4).
	if sesudah := store.Outbox().PendingCount(); sesudah < sebelum {
		t.Fatalf("outbox menyusut saat offline (%d → %d) — pekerjaan hilang, bukan tertunda",
			sebelum, sesudah)
	}
}

// ── Gabungan: semua rusak sekaligus ──

// Kerusakan jarang datang sendirian: listrik satu rak mematikan controller DAN router.
//
// Yang diperiksa di sini bukan tiap kegagalan satu per satu — itu sudah di atas —
// melainkan bahwa gabungannya tidak menghasilkan kegagalan baru: lahan tetap melayani
// lewat gerbang yang perangkatnya masih utuh.
func TestChaosSemuaRusakSekaligus(t *testing.T) {
	svc, sd, store := lahanCampuran(t)
	store.AddMember("04D4E5F6", []string{"D5678XYZ"}, "mobil", time.Now().AddDate(1, 0, 0))

	r1, _ := svc.Gate("GATE-IN-01")
	tungguHingga(t, func() bool {
		return r1.StatusPerangkat() == tcpctl.StatusOnline.String()
	}, "controller tersambung")

	// LAN dicabut + kertas habis di gerbang lain + internet memang tak ada.
	sd.PutusKoneksi()
	_ = sd.Close()

	r2, _ := svc.Gate("GATE-IN-02")
	r2.Sim().Printer.SetStatus(hw.PrinterPaperOut)

	tungguHingga(t, func() bool {
		return r1.StatusPerangkat() != tcpctl.StatusOnline.String()
	}, "gerbang nyata terputus")

	// Member masih bisa masuk lewat gerbang yang kertasnya habis (D3), dan gerbang
	// keluar — jalur pendapatan — tetap hidup sepenuhnya.
	if err := r2.TapRFID("04D4E5F6"); err != nil {
		t.Fatalf("tap member saat semuanya rusak: %v", err)
	}
	if st := r2.State(); st == string(gate.StateLockedNoPaper) {
		t.Fatal("member terkunci — D3 tak berlaku saat kegagalan menumpuk")
	}

	rOut, _ := svc.Gate("GATE-OUT-01")
	if h := rOut.Health(time.Second); h.Status != HealthOK {
		t.Fatalf("gerbang keluar = %q, want %q — jalur pendapatan ikut tumbang", h.Status, HealthOK)
	}

	// Proses tetap sehat: tak satu pun kerusakan lapangan boleh membuat watchdog
	// membunuh edge-api, sebab restart tak memperbaiki kabel maupun kertas (K33).
	tungguHingga(t, func() bool { return !svc.LastHealthSweep().IsZero() }, "healthcheck menyapu")
	if !svc.MesinInternalHidup() {
		t.Fatal("kerusakan lapangan membekukan penilaian mesin internal")
	}
}
