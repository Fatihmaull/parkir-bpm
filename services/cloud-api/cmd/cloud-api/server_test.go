package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// testAuditHash — replika formula audit.computeHash (edge-api) / store.computeHash (cloud-api,
// tak diekspor) HANYA untuk membangun payload uji yang sah lewat HTTP; lihat komentar duplikasi
// formula di internal/store/audit.go.
func testAuditHash(prevHash, nodeID string, seq int64, eventType, actorID, payloadJSON string, createdAt time.Time) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x1f%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s",
		prevHash, nodeID, seq, eventType, actorID, payloadJSON, createdAt.Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil))
}

func TestAuditBatchSyncAndVerifyEndToEnd(t *testing.T) {
	app := testApp(t)
	tok := login(t, app)

	const genesis = "0000000000000000000000000000000000000000000000000000000000000000"
	now := time.Now()
	payloadJSON := `{"gate":"IN-1"}`
	h1 := testAuditHash(genesis, "node-a", 1, "GATE_OPEN", "op-1", payloadJSON, now)

	batch := fmt.Sprintf(`{"tenant_id":"t-jabar","entries":[{
		"site_id":"s-1","node_id":"node-a","seq":1,"event_type":"GATE_OPEN","severity":"normal",
		"actor_id":"op-1","actor_label":"Operator 1","actor_role":"Kasir","gate_label":"IN-1",
		"summary":"buka palang","payload":%s,
		"previous_hash":"%s","current_hash":"%s","created_at":"%s"}]}`,
		payloadJSON, genesis, h1, now.Format(time.RFC3339Nano))

	code, m := do(t, app, "POST", "/internal/v1/sync/audit-batch", "", batch)
	if code != 200 || m["applied"].(float64) != 1 {
		t.Fatalf("audit-batch: %d %v", code, m)
	}

	// Tanpa token → ditolak (endpoint /api/v1/audit butuh role SuperAdmin/Auditor).
	if code, _ := do(t, app, "GET", "/api/v1/audit", "", ""); code != 401 {
		t.Fatalf("audit tanpa token harus 401, got %d", code)
	}

	_, am := do(t, app, "GET", "/api/v1/audit", tok, "")
	chains := am["chains"].([]any)
	if len(chains) != 1 || chains[0].(map[string]any)["last_seq"].(float64) != 1 {
		t.Fatalf("chains tak sesuai: %v", am)
	}

	_, vm := do(t, app, "POST", "/api/v1/audit/verify", tok, `{}`)
	if vm["verified"] != true {
		t.Fatalf("verifikasi rantai utuh harus true: %v", vm)
	}
}
