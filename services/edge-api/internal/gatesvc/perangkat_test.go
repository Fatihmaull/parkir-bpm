package gatesvc

import (
	"errors"
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl/simdev"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// svcNyata menjalankan Service dengan gerbang bertransport tcp di atas simulator
// controller A6/A9 (task 1.10) — bukan simulator perangkat Layer-2.
func svcNyata(t *testing.T, kind GateKind) (*Service, *simdev.Device) {
	t.Helper()

	sd, err := simdev.New("", simdev.WithPulse(30*time.Millisecond))
	if err != nil {
		t.Fatalf("simdev.New: %v", err)
	}
	t.Cleanup(func() { _ = sd.Close() })

	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	kode := "GATE-IN-01"
	if kind == KindExit {
		kode = "GATE-OUT-01"
	}
	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site:   gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source: StaticSource{{Code: kode, Kind: kind, Transport: TransportTCP, Endpoint: sd.Addr()}},
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc, sd
}

func tungguHingga(t *testing.T, cek func() bool, apa string) {
	t.Helper()
	batas := time.Now().Add(5 * time.Second)
	for time.Now().Before(batas) {
		if cek() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("timeout menunggu: %s", apa)
}

// Inti task 2.6: gerbang bertransport tcp benar-benar memerintah controller nyata.
func TestGerbangTCPMemerintahControllerNyata(t *testing.T) {
	svc, sd := svcNyata(t, KindExit)
	g, _ := svc.Gate("GATE-OUT-01")

	if !g.Nyata() {
		t.Fatal("gerbang tcp seharusnya ditandai nyata")
	}
	tungguHingga(t, func() bool { return g.StatusPerangkat() == "ONLINE" }, "controller ONLINE")

	// Kendaraan tiba di loop keluar → state machine bergerak dari sinyal PERANGKAT,
	// bukan dari suntikan HTTP.
	if err := sd.SetInput(1, true); err != nil { // LD3 = IN1
		t.Fatalf("SetInput: %v", err)
	}
	tungguHingga(t, func() bool { return g.State() == string(gate.XIdentifying) },
		"state bergerak oleh sinyal loop nyata")
}

// Tutup palang dipicu sensor: LD4 naik lalu turun → Edge mengirim OUT1OFF.
func TestTutupDipicuSensorPadaControllerNyata(t *testing.T) {
	svc, sd := svcNyata(t, KindExit)
	g, _ := svc.Gate("GATE-OUT-01")
	tungguHingga(t, func() bool { return g.StatusPerangkat() == "ONLINE" }, "controller ONLINE")

	// Buka palang lewat state machine (jalur bayar disederhanakan: paksa OPEN via LD3+bayar
	// tak dilalui di sini; cukup perintahkan barrier lewat perangkat Layer-2).
	if err := g.FireExit(gate.XEvent{Kind: gate.XEvLD3, High: true}); err != nil {
		t.Fatalf("LD3: %v", err)
	}

	// Kendaraan melewati loop bawah: naik lalu turun.
	if err := sd.SetInput(2, true); err != nil { // LD4 = IN2
		t.Fatalf("LD4 naik: %v", err)
	}
	tungguHingga(t, func() bool { return sd.Input(2) }, "LD4 tercatat HIGH di controller")
	if err := sd.SetInput(2, false); err != nil {
		t.Fatalf("LD4 turun: %v", err)
	}
	tungguHingga(t, func() bool { return !sd.Input(2) }, "LD4 tercatat LOW di controller")

	// Relay palang harus berakhir padam — Edge tak pernah membiarkannya menyala.
	tungguHingga(t, func() bool { return !sd.Output(1) }, "relay palang padam")
}

// Gerbang masuk nyata masih memakai printer simulasi (task 4.1 terblokir H1) — dan itu
// WAJIB terlihat, bukan tersamar.
func TestGerbangMasukNyataMengumumkanPrinterSimulasi(t *testing.T) {
	svc, _ := svcNyata(t, KindEntry)
	g, _ := svc.Gate("GATE-IN-01")

	palsu := g.Disimulasikan()
	if len(palsu) != 1 || palsu[0] != "printer" {
		t.Fatalf("Disimulasikan() = %v, want [printer]", palsu)
	}
}

// Gerbang keluar tak butuh printer, jadi tak ada perangkat palsu tersisa.
func TestGerbangKeluarNyataTanpaPerangkatPalsu(t *testing.T) {
	svc, _ := svcNyata(t, KindExit)
	g, _ := svc.Gate("GATE-OUT-01")

	if palsu := g.Disimulasikan(); len(palsu) != 0 {
		t.Fatalf("Disimulasikan() = %v, want kosong", palsu)
	}
}

// Loop pada gerbang nyata digerakkan kendaraan, bukan HTTP.
func TestInjeksiLoopDitolakPadaGerbangNyata(t *testing.T) {
	svc, _ := svcNyata(t, KindExit)
	g, _ := svc.Gate("GATE-OUT-01")

	if err := g.DriveLoop("pre", true); !errors.Is(err, ErrBukanSimulator) {
		t.Fatalf("DriveLoop pada gerbang nyata = %v, want ErrBukanSimulator", err)
	}
	if g.Sim() != nil {
		t.Fatal("Sim() harus nil untuk gerbang nyata")
	}
}

// Gerbang tersimulasi tetap berperilaku seperti sebelumnya.
func TestGerbangSimTetapBisaDisuntik(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())
	g, _ := svc.Gate("GATE-IN-01")

	if g.Nyata() {
		t.Fatal("gerbang sim tidak boleh ditandai nyata")
	}
	if g.StatusPerangkat() != "" {
		t.Fatalf("StatusPerangkat = %q, want kosong untuk simulator", g.StatusPerangkat())
	}
	if err := g.DriveLoop("pre", true); err != nil {
		t.Fatalf("DriveLoop pada gerbang sim: %v", err)
	}
}

// Endpoint tcp yang mati tidak boleh menggagalkan startup — driver menyambung sendiri
// dengan backoff (P8: lahan tetap hidup walau satu controller belum siap).
func TestGerbangTCPTetapStartWalauControllerBelumAda(t *testing.T) {
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)

	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Source: StaticSource{{
			Code: "GATE-OUT-01", Kind: KindExit,
			Transport: TransportTCP, Endpoint: "127.0.0.1:1",
		}},
	}, hub, store)
	if err != nil {
		t.Fatalf("New seharusnya berhasil walau controller mati: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)

	g, _ := svc.Gate("GATE-OUT-01")
	if got := g.StatusPerangkat(); got == "ONLINE" {
		t.Fatalf("StatusPerangkat = %q, want bukan ONLINE", got)
	}
}
