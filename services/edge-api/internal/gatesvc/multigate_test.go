package gatesvc

import (
	"errors"
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// duaMasukSatuKeluar — lahan dengan dua gerbang masuk, persis bentuk yang tak dapat
// diwakili model "entry/exit tunggal" sebelumnya.
func duaMasukSatuKeluar() StaticSource {
	return StaticSource{
		{Code: "GATE-IN-01", Kind: KindEntry, Transport: TransportSim},
		{Code: "GATE-IN-02", Kind: KindEntry, Transport: TransportSim},
		{Code: "GATE-OUT-01", Kind: KindExit, Transport: TransportSim},
	}
}

func svcDengan(t *testing.T, src GateSource) (*Service, *wsbus.Hub) {
	t.Helper()
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site:   gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source: src,
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc, hub
}

func TestServiceMemuatNGerbang(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())

	specs := svc.Specs()
	if len(specs) != 3 {
		t.Fatalf("jumlah gerbang = %d, want 3", len(specs))
	}
	// Urutan harus mengikuti sumber, bukan urutan map.
	want := []string{"GATE-IN-01", "GATE-IN-02", "GATE-OUT-01"}
	for i, w := range want {
		if specs[i].Code != w {
			t.Fatalf("gerbang %d = %q, want %q", i, specs[i].Code, w)
		}
	}
	for _, code := range want {
		if _, ok := svc.Gate(code); !ok {
			t.Fatalf("gerbang %q tak ditemukan", code)
		}
	}
	if _, ok := svc.Gate("GATE-TIDAK-ADA"); ok {
		t.Fatal("gerbang tak dikenal seharusnya tidak ditemukan")
	}
}

// Inti task 2.1: gerbang saling bebas. Menggerakkan satu gerbang masuk tidak boleh
// menyeret gerbang masuk lainnya.
func TestGerbangSalingBebas(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())

	g1, _ := svc.Gate("GATE-IN-01")
	g2, _ := svc.Gate("GATE-IN-02")

	awal := g2.State()
	if err := g1.DriveLoop("pre", true); err != nil {
		t.Fatalf("DriveLoop: %v", err)
	}

	if g1.State() == awal {
		t.Fatalf("state GATE-IN-01 tidak bergerak: %q", g1.State())
	}
	if got := g2.State(); got != awal {
		t.Fatalf("state GATE-IN-02 ikut berubah jadi %q, want tetap %q", got, awal)
	}
}

// Inti task 2.2: setiap event membawa gate_code, sehingga dua gerbang masuk dapat
// dibedakan di dashboard maupun log.
func TestEventBerlabelGateCode(t *testing.T) {
	svc, hub := svcDengan(t, duaMasukSatuKeluar())

	ch, unsub := hub.Subscribe()
	defer unsub()

	g2, _ := svc.Gate("GATE-IN-02")
	if err := g2.DriveLoop("pre", true); err != nil {
		t.Fatalf("DriveLoop: %v", err)
	}

	tenggat := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			code, ada := ev.Data["gate_code"]
			if !ada {
				continue // event lain (mis. lpr.result diuji terpisah)
			}
			if code != "GATE-IN-02" {
				t.Fatalf("gate_code = %v, want GATE-IN-02", code)
			}
			if kind := ev.Data["gate_kind"]; kind != string(KindEntry) {
				t.Fatalf("gate_kind = %v, want ENTRY", kind)
			}
			return
		case <-tenggat:
			t.Fatal("timeout menunggu event berlabel gate_code")
		}
	}
}

func TestAksiSalahJenisGerbangDitolak(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())

	masuk, _ := svc.Gate("GATE-IN-01")
	keluar, _ := svc.Gate("GATE-OUT-01")

	if err := masuk.FireExit(gate.XEvent{Kind: gate.XEvPhotoVerdict}); !errors.Is(err, ErrJenisGerbang) {
		t.Fatalf("FireExit di gerbang masuk = %v, want ErrJenisGerbang", err)
	}
	if err := keluar.FireEntry(gate.Event{Kind: gate.EvButton}); !errors.Is(err, ErrJenisGerbang) {
		t.Fatalf("FireEntry di gerbang keluar = %v, want ErrJenisGerbang", err)
	}
	if err := keluar.TakeTicket(); !errors.Is(err, ErrJenisGerbang) {
		t.Fatalf("TakeTicket di gerbang keluar = %v, want ErrJenisGerbang", err)
	}
	if _, err := masuk.Amount(); !errors.Is(err, ErrJenisGerbang) {
		t.Fatalf("Amount di gerbang masuk = %v, want ErrJenisGerbang", err)
	}
}

// EntryGate/ExitGate harus menunjuk gerbang PERTAMA tiap jenis — itulah yang membuat
// endpoint lama tetap berfungsi selama dashboard belum bermigrasi.
func TestGerbangPertamaTiapJenis(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())

	g1, _ := svc.Gate("GATE-IN-01")
	if svc.EntryGate() != g1 {
		t.Fatalf("EntryGate = %q, want GATE-IN-01", svc.EntryGate().Spec().Code)
	}
	if got := svc.ExitGate().Spec().Code; got != "GATE-OUT-01" {
		t.Fatalf("ExitGate = %q, want GATE-OUT-01", got)
	}

	// Lahan tanpa gerbang keluar tak boleh membuat pemanggil panik.
	hanyaMasuk, _ := svcDengan(t, StaticSource{{Code: "G1", Kind: KindEntry, Transport: TransportSim}})
	if hanyaMasuk.ExitGate() != nil {
		t.Fatal("ExitGate seharusnya nil bila tak ada gerbang keluar")
	}
}

func TestNewMenolakDaftarGerbangTakSah(t *testing.T) {
	cases := []struct {
		nama string
		src  StaticSource
	}{
		{"kosong", StaticSource{}},
		{"code kembar", StaticSource{
			{Code: "G1", Kind: KindEntry, Transport: TransportSim},
			{Code: "G1", Kind: KindExit, Transport: TransportSim},
		}},
		{"tanpa code", StaticSource{{Kind: KindEntry, Transport: TransportSim}}},
		{"kind tak dikenal", StaticSource{{Code: "G1", Kind: "GERBANG", Transport: TransportSim}}},
		{"transport tak dikenal", StaticSource{{Code: "G1", Kind: KindEntry, Transport: "serial"}}},
		{"tcp tanpa endpoint", StaticSource{{Code: "G1", Kind: KindEntry, Transport: TransportTCP}}},
	}

	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	for _, tc := range cases {
		t.Run(tc.nama, func(t *testing.T) {
			if _, err := New(Config{Source: tc.src}, hub, store); err == nil {
				t.Fatalf("New menerima daftar gerbang %s", tc.nama)
			}
		})
	}
}

func TestNewTanpaSourcePakaiBawaan(t *testing.T) {
	svc, _ := svcDengan(t, nil)

	specs := svc.Specs()
	if len(specs) != 2 {
		t.Fatalf("jumlah gerbang bawaan = %d, want 2", len(specs))
	}
	if specs[0].Kind != KindEntry || specs[1].Kind != KindExit {
		t.Fatalf("bawaan = %+v, want satu ENTRY lalu satu EXIT", specs)
	}
}

func TestSpecsFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.GateIn.Transport = "tcp"
	cfg.GateIn.Endpoint = "10.0.0.5:56001"
	cfg.GateOut.Transport = "sim"

	got := SpecsFromConfig(cfg)
	if len(got) != 2 {
		t.Fatalf("jumlah = %d, want 2", len(got))
	}
	if got[0].Transport != TransportTCP || got[0].Endpoint != "10.0.0.5:56001" {
		t.Fatalf("gerbang masuk = %+v", got[0])
	}
	if got[1].Transport != TransportSim {
		t.Fatalf("gerbang keluar = %+v", got[1])
	}
	if err := ValidateSpecs(got); err != nil {
		t.Fatalf("hasil SpecsFromConfig tidak lolos validasi: %v", err)
	}

	// Transport yang tak dikenal jatuh ke simulator, bukan menghasilkan spec tak sah.
	cfg.GateIn.Transport = "serial"
	if got := SpecsFromConfig(cfg); got[0].Transport != TransportSim {
		t.Fatalf("transport tak dikenal = %q, want sim", got[0].Transport)
	}
	if err := ValidateSpecs(SpecsFromConfig(nil)); err != nil {
		t.Fatalf("SpecsFromConfig(nil) tidak lolos validasi: %v", err)
	}
}

func TestCloseIdempotenDanMenghentikanPemilik(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())

	svc.Close()
	svc.Close() // tak boleh panik

	// Setelah berhenti, aksi ditolak alih-alih menggantung.
	g, _ := svc.Gate("GATE-IN-01")
	if err := g.FireEntry(gate.Event{Kind: gate.EvButton}); !errors.Is(err, ErrBerhenti) {
		t.Fatalf("FireEntry setelah Close = %v, want ErrBerhenti", err)
	}
}

// Siklus masuk penuh harus tetap berjalan di gerbang mana pun, bukan hanya yang pertama.
func TestSiklusMasukDiGerbangKedua(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())
	g2, _ := svc.Gate("GATE-IN-02")

	if err := g2.DriveLoop("pre", true); err != nil {
		t.Fatalf("LD1: %v", err)
	}
	if err := g2.FireEntry(gate.Event{Kind: gate.EvButton}); err != nil {
		t.Fatalf("tombol: %v", err)
	}
	if err := g2.TakeTicket(); err != nil {
		t.Fatalf("ambil tiket: %v", err)
	}
	if got := g2.State(); got != string(gate.StateOpen) {
		t.Fatalf("state GATE-IN-02 = %q, want %q", got, gate.StateOpen)
	}
	// Gerbang pertama tak boleh ikut terbuka.
	g1, _ := svc.Gate("GATE-IN-01")
	if got := g1.State(); got == string(gate.StateOpen) {
		t.Fatalf("GATE-IN-01 ikut terbuka: %q", got)
	}
}
