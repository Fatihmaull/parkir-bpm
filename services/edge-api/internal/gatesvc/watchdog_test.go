package gatesvc

import (
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// svcPengaman menyiapkan Service dengan ambang pengaman yang dipercepat.
func svcPengaman(t *testing.T, blocked, stalled time.Duration) (*Service, *wsbus.Hub) {
	t.Helper()
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site:         gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source:       duaMasukSatuKeluar(),
		BlockedAfter: blocked,
		StalledAfter: stalled,
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc, hub
}

// tungguAlert menunggu alert dengan jenis tertentu muncul di bus.
func tungguAlert(t *testing.T, ch <-chan wsbus.Event, jenis, gateCode string) map[string]any {
	t.Helper()
	tenggat := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Name != "alert.raised" {
				continue
			}
			if ev.Data["type"] == jenis && ev.Data["gate_code"] == gateCode {
				return ev.Data
			}
		case <-tenggat:
			t.Fatalf("timeout menunggu alert %s dari %s", jenis, gateCode)
			return nil
		}
	}
}

func takAdaAlert(t *testing.T, ch <-chan wsbus.Event, jenis string, selama time.Duration) {
	t.Helper()
	tenggat := time.After(selama)
	for {
		select {
		case ev := <-ch:
			if ev.Name == "alert.raised" && ev.Data["type"] == jenis {
				t.Fatalf("alert %s muncul padahal tidak seharusnya: %v", jenis, ev.Data)
			}
		case <-tenggat:
			return
		}
	}
}

// Kendaraan tersangkut di bawah palang → BARRIER_BLOCKED critical (PRD v2 §5.4).
func TestBarrierBlockedSaatLoopBawahHIGHTerlaluLama(t *testing.T) {
	svc, hub := svcPengaman(t, 40*time.Millisecond, time.Hour)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-IN-01")
	if err := g.FireEntry(gate.Event{Kind: gate.EvLD2, High: true}); err != nil {
		t.Fatalf("LD2: %v", err)
	}

	data := tungguAlert(t, ch, AlertBarrierBlocked, "GATE-IN-01")
	if data["severity"] != "critical" {
		t.Fatalf("severity = %v, want critical", data["severity"])
	}
}

// Loop bawah yang turun sebelum ambang harus membatalkan alert.
func TestBarrierBlockedBatalSaatLoopTurun(t *testing.T) {
	svc, hub := svcPengaman(t, 300*time.Millisecond, time.Hour)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-IN-01")
	if err := g.FireEntry(gate.Event{Kind: gate.EvLD2, High: true}); err != nil {
		t.Fatalf("LD2 naik: %v", err)
	}
	if err := g.FireEntry(gate.Event{Kind: gate.EvLD2, High: false}); err != nil {
		t.Fatalf("LD2 turun: %v", err)
	}

	takAdaAlert(t, ch, AlertBarrierBlocked, 500*time.Millisecond)
}

// Kendaraan diam di loop kehadiran tanpa menyelesaikan siklus → VEHICLE_STALLED.
func TestVehicleStalledSaatLoopDepanHIGHTerlaluLama(t *testing.T) {
	svc, hub := svcPengaman(t, time.Hour, 40*time.Millisecond)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-IN-01")
	if err := g.FireEntry(gate.Event{Kind: gate.EvLD1, High: true}); err != nil {
		t.Fatalf("LD1: %v", err)
	}

	data := tungguAlert(t, ch, AlertVehicleStalled, "GATE-IN-01")
	if data["severity"] != "warning" {
		t.Fatalf("severity = %v, want warning", data["severity"])
	}
}

// Palang yang sudah terbuka berarti siklus berjalan normal — bukan kendaraan mogok.
func TestVehicleStalledTidakMunculSaatPalangTerbuka(t *testing.T) {
	svc, hub := svcPengaman(t, time.Hour, 150*time.Millisecond)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-IN-01")
	if err := g.FireEntry(gate.Event{Kind: gate.EvLD1, High: true}); err != nil {
		t.Fatalf("LD1: %v", err)
	}
	if err := g.FireEntry(gate.Event{Kind: gate.EvButton}); err != nil {
		t.Fatalf("tombol: %v", err)
	}
	if err := g.TakeTicket(); err != nil {
		t.Fatalf("ambil tiket: %v", err)
	}
	if got := g.State(); got != string(gate.StateOpen) {
		t.Fatalf("prasyarat: state = %q, want OPEN", got)
	}

	takAdaAlert(t, ch, AlertVehicleStalled, 400*time.Millisecond)
}

// Loop bawah naik padahal palang tak pernah dibuka → palang ditembus (critical).
func TestUnauthorizedPassage(t *testing.T) {
	svc, hub := svcPengaman(t, time.Hour, time.Hour)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-IN-01")
	if err := g.FireEntry(gate.Event{Kind: gate.EvLD2, High: true}); err != nil {
		t.Fatalf("LD2: %v", err)
	}

	data := tungguAlert(t, ch, AlertUnauthorizedPassage, "GATE-IN-01")
	if data["severity"] != "critical" {
		t.Fatalf("severity = %v, want critical", data["severity"])
	}
}

// Lintasan yang sah (palang sudah terbuka) tidak boleh dianggap pelanggaran.
func TestUnauthorizedPassageTidakMunculSaatLintasanSah(t *testing.T) {
	svc, hub := svcPengaman(t, time.Hour, time.Hour)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-IN-01")
	for _, langkah := range []func() error{
		func() error { return g.FireEntry(gate.Event{Kind: gate.EvLD1, High: true}) },
		func() error { return g.FireEntry(gate.Event{Kind: gate.EvButton}) },
		g.TakeTicket,
	} {
		if err := langkah(); err != nil {
			t.Fatalf("persiapan siklus: %v", err)
		}
	}

	if err := g.FireEntry(gate.Event{Kind: gate.EvLD2, High: true}); err != nil {
		t.Fatalf("LD2: %v", err)
	}
	takAdaAlert(t, ch, AlertUnauthorizedPassage, 300*time.Millisecond)
}

// Alert harus menyebut gerbang asalnya — lahan bisa punya beberapa gerbang masuk.
func TestAlertBerlabelGerbangAsal(t *testing.T) {
	svc, hub := svcPengaman(t, 40*time.Millisecond, time.Hour)
	ch, un := hub.Subscribe()
	defer un()

	g2, _ := svc.Gate("GATE-IN-02")
	if err := g2.FireEntry(gate.Event{Kind: gate.EvLD2, High: true}); err != nil {
		t.Fatalf("LD2: %v", err)
	}

	data := tungguAlert(t, ch, AlertBarrierBlocked, "GATE-IN-02")
	if data["gate_kind"] != string(KindEntry) {
		t.Fatalf("gate_kind = %v, want ENTRY", data["gate_kind"])
	}
}

// Gerbang keluar memakai jalur LD3/LD4 dan harus terlindungi sama.
func TestPengamanBerlakuDiGerbangKeluar(t *testing.T) {
	svc, hub := svcPengaman(t, 40*time.Millisecond, time.Hour)
	ch, un := hub.Subscribe()
	defer un()

	g, _ := svc.Gate("GATE-OUT-01")
	if err := g.FireExit(gate.XEvent{Kind: gate.XEvLD4, High: true}); err != nil {
		t.Fatalf("LD4: %v", err)
	}

	tungguAlert(t, ch, AlertBarrierBlocked, "GATE-OUT-01")
}

func TestAmbangBawaanPengaman(t *testing.T) {
	if DefaultBlockedAfter != 120*time.Second || DefaultStalledAfter != 120*time.Second {
		t.Fatalf("ambang bawaan = (%v,%v), PRD mensyaratkan 120 dtk",
			DefaultBlockedAfter, DefaultStalledAfter)
	}
}
