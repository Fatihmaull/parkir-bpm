package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

func testApp(t *testing.T) (*fiber.App, *gatesvc.Service) {
	t.Helper()
	hub := wsbus.NewHub()
	store := memstore.New("node-http", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})
	svc, err := gatesvc.New(gatesvc.Config{
		NodeID: "node-http", TenantID: "t1", SiteID: "s1",
		Site: gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
	}, hub, store)
	if err != nil {
		t.Fatalf("gatesvc.New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	app := fiber.New()
	registerRoutes(app, &config.Config{NodeID: "node-http", Env: "test"}, svc, hub)
	return app, svc
}

func post(t *testing.T, app *fiber.App, path, body string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if resp.StatusCode != 200 {
		t.Fatalf("POST %s status %d: %s", path, resp.StatusCode, string(b))
	}
	return m
}

func TestHealthEndpoint(t *testing.T) {
	app, _ := testApp(t)
	req, _ := http.NewRequest("GET", "/api/v1/health", nil)
	resp, err := app.Test(req, 2000)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: err=%v status=%d", err, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["status"] != "ok" {
		t.Fatalf("health status: %v", m)
	}
}

// Regresi: endpoint simulator harus membalas state SETELAH event diproses, bukan state basi.
// Dulu handler memanggil sim.Drive (asinkron lewat goroutine forwarder) lalu langsung membaca
// State(), sehingga balasan bisa berisi IDLE dan aksi berikutnya tiba terlalu cepat.
func TestSimLoopEndpointReturnsSettledState(t *testing.T) {
	app, svc := testApp(t)

	m := post(t, app, "/api/v1/sim/exit/loop", `{"loop":"pre","high":true}`)
	if m["exit_state"] != "IDENTIFYING" {
		t.Fatalf("balasan LD3 harus IDENTIFYING (bukan state basi), got %v", m["exit_state"])
	}
	if gate.XState(svc.ExitGate().State()) != gate.XIdentifying {
		t.Fatalf("state service setelah LD3 = %s, harus IDENTIFYING", gate.XState(svc.ExitGate().State()))
	}

	m = post(t, app, "/api/v1/sim/entry/loop", `{"loop":"pre","high":true}`)
	if m["entry_state"] == "IDLE" {
		t.Fatalf("balasan LD1 masih IDLE — state basi")
	}
}

// Integrasi HTTP: siapkan kendaraan IN_PREMISES via entry (sync), lalu jalankan siklus KELUAR
// penuh lewat endpoint HTTP dan pastikan palang keluar terbuka setelah bayar tunai.
func TestExitCycleOverHTTP(t *testing.T) {
	app, svc := testApp(t)

	// Buat satu kendaraan masuk (sinkron) → menghasilkan tiket TKT-000001, status IN_PREMISES.
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD1, High: true})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvButton})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvTicketTaken})
	if gate.State(svc.EntryGate().State()) != gate.StateOpen {
		t.Fatalf("persiapan: entry harus OPEN, got %s", gate.State(svc.EntryGate().State()))
	}

	// LD3 (kendaraan tiba di gerbang keluar).
	post(t, app, "/api/v1/sim/exit/loop", `{"loop":"pre","high":true}`)
	// Identifikasi via tiket.
	m := post(t, app, "/api/v1/sim/exit/identify", `{"ticket":"TKT-000001"}`)
	if m["exit_state"] != "TRANSACTION_FOUND" {
		t.Fatalf("identify: exit_state=%v", m["exit_state"])
	}
	// Komparasi foto cocok.
	post(t, app, "/api/v1/sim/exit/photo", `{"match":true}`)
	// Bayar tunai (uang cukup).
	m = post(t, app, "/api/v1/sim/exit/pay", `{"method":"CASH","tendered":20000}`)
	if m["exit_state"] != "OPEN" {
		t.Fatalf("setelah bayar: exit_state=%v (harus OPEN)", m["exit_state"])
	}
}
