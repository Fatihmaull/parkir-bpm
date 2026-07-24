package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/cloud-api/internal/auth"
	"github.com/jabar-creative/parkir/cloud-api/internal/store"
)

func testApp(t *testing.T) *fiber.App {
	t.Helper()
	iss := auth.NewIssuer("access-secret-min-32-chars-aaaaaaaa", "refresh-secret-min-32-chars-bbbbb", time.Minute, time.Hour)
	st := store.New()
	seed(st)
	app := fiber.New()
	registerRoutes(app, st, iss)
	return app
}

func do(t *testing.T, app *fiber.App, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func login(t *testing.T, app *fiber.App) string {
	t.Helper()
	code, m := do(t, app, "POST", "/api/v1/auth/login", "", `{"Email":"admin@parkir.local","Password":"admin12345"}`)
	if code != 200 {
		t.Fatalf("login gagal: %d %v", code, m)
	}
	return m["access_token"].(string)
}

func TestLoginSuccessAndWrongPassword(t *testing.T) {
	app := testApp(t)
	if tok := login(t, app); tok == "" {
		t.Fatal("access_token kosong")
	}
	code, _ := do(t, app, "POST", "/api/v1/auth/login", "", `{"Email":"admin@parkir.local","Password":"salah"}`)
	if code != 401 {
		t.Fatalf("password salah harus 401, got %d", code)
	}
}

func TestProtectedRequiresToken(t *testing.T) {
	app := testApp(t)
	code, _ := do(t, app, "GET", "/api/v1/sites", "", "")
	if code != 401 {
		t.Fatalf("tanpa token harus 401, got %d", code)
	}
}

func TestTenantIsolationSites(t *testing.T) {
	app := testApp(t)
	tok := login(t, app)
	code, m := do(t, app, "GET", "/api/v1/sites", tok, "")
	if code != 200 {
		t.Fatalf("sites: %d", code)
	}
	sites := m["sites"].([]any)
	// Admin t-jabar HANYA melihat 2 site tenantnya, BUKAN site t-other.
	if len(sites) != 2 {
		t.Fatalf("harus 2 site tenant sendiri, got %d (%v)", len(sites), sites)
	}
	for _, s := range sites {
		if s.(map[string]any)["tenant_id"] != "t-jabar" {
			t.Fatalf("bocor lintas tenant: %v", s)
		}
	}
}

func TestSyncBatchIdempotent(t *testing.T) {
	app := testApp(t)
	tok := login(t, app)
	batch := `{"tenant_id":"t-jabar","items":[
		{"aggregate_id":"v1","aggregate":"vehicles_log","payload":{"amount":10000}},
		{"aggregate_id":"v2","aggregate":"vehicles_log","payload":{"amount":5000}}]}`

	code, m := do(t, app, "POST", "/internal/v1/sync/batch", "", batch)
	if code != 200 || m["applied"].(float64) != 2 {
		t.Fatalf("batch pertama: %d applied=%v", code, m["applied"])
	}
	// Kirim ulang batch sama → semua dilewati (idempoten §10.3).
	_, m = do(t, app, "POST", "/internal/v1/sync/batch", "", batch)
	if m["applied"].(float64) != 0 || m["skipped"].(float64) != 2 {
		t.Fatalf("pengiriman ulang harus 0 applied/2 skipped, got %v", m)
	}
	// Transaksi tenant t-jabar kini 2.
	_, tm := do(t, app, "GET", "/api/v1/transactions", tok, "")
	if len(tm["transactions"].([]any)) != 2 {
		t.Fatalf("harus 2 transaksi ter-sync, got %v", tm["transactions"])
	}
}
