package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// getJSON menjalankan GET dan mengembalikan body sebagai map.
func getJSON(t *testing.T, app *fiber.App, path string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", path, nil)
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s status %d: %s", path, resp.StatusCode, string(b))
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// getStatus menjalankan GET dan mengembalikan kode statusnya saja.
func getStatus(t *testing.T, app *fiber.App, path string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", path, nil)
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode
}

// statusPost menjalankan POST dan mengembalikan kode statusnya saja.
func statusPost(t *testing.T, app *fiber.App, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp.StatusCode
}

func TestDaftarGerbangHTTP(t *testing.T) {
	app, _ := testApp(t)

	m := getJSON(t, app, "/api/v1/gates")
	gates, ok := m["gates"].([]any)
	if !ok || len(gates) != 2 {
		t.Fatalf("gates = %v, want 2 entri", m["gates"])
	}
	first, _ := gates[0].(map[string]any)
	if first["code"] != "GATE-IN-01" || first["kind"] != "ENTRY" {
		t.Fatalf("gerbang pertama = %v", first)
	}
	if first["state"] == nil {
		t.Fatalf("gerbang pertama tanpa state: %v", first)
	}
}

// Siklus masuk penuh lewat endpoint beralamat gate_code (task 2.2).
func TestSiklusMasukLewatGateCode(t *testing.T) {
	app, _ := testApp(t)
	const g = "/api/v1/sim/gates/GATE-IN-01"

	m := post(t, app, g+"/loop", `{"loop":"pre","high":true}`)
	if m["gate_code"] != "GATE-IN-01" {
		t.Fatalf("balasan tanpa gate_code yang benar: %v", m)
	}
	post(t, app, g+"/button", `{}`)
	m = post(t, app, g+"/ticket-taken", `{}`)
	if m["state"] != "OPEN" {
		t.Fatalf("state = %v, want OPEN", m["state"])
	}

	// State gerbang juga terbaca lewat endpoint beralamat code.
	s := getJSON(t, app, "/api/v1/gates/GATE-IN-01/state")
	if s["state"] != "OPEN" {
		t.Fatalf("state = %v, want OPEN", s["state"])
	}
}

func TestSiklusKeluarLewatGateCode(t *testing.T) {
	app, _ := testApp(t)
	const masuk = "/api/v1/sim/gates/GATE-IN-01"
	const keluar = "/api/v1/sim/gates/GATE-OUT-01"

	// Siapkan satu kendaraan di dalam.
	post(t, app, masuk+"/loop", `{"loop":"pre","high":true}`)
	post(t, app, masuk+"/button", `{}`)
	post(t, app, masuk+"/ticket-taken", `{}`)

	post(t, app, keluar+"/loop", `{"loop":"pre","high":true}`)
	m := post(t, app, keluar+"/identify", `{"ticket":"TKT-000001"}`)
	if m["state"] != "TRANSACTION_FOUND" {
		t.Fatalf("identify: state = %v", m["state"])
	}
	post(t, app, keluar+"/photo", `{"match":true}`)
	m = post(t, app, keluar+"/pay", `{"method":"CASH","tendered":20000}`)
	if m["state"] != "OPEN" {
		t.Fatalf("setelah bayar: state = %v, want OPEN", m["state"])
	}
}

func TestGateCodeTakDikenalBalas404(t *testing.T) {
	app, _ := testApp(t)

	if got := statusPost(t, app, "/api/v1/sim/gates/GATE-HANTU/loop", `{"loop":"pre","high":true}`); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// Aksi yang salah alamat adalah kesalahan permintaan, bukan kegagalan server.
func TestAksiSalahJenisGerbangBalas409(t *testing.T) {
	app, _ := testApp(t)

	if got := statusPost(t, app, "/api/v1/sim/gates/GATE-OUT-01/ticket-taken", `{}`); got != 409 {
		t.Fatalf("ambil tiket di gerbang keluar: status = %d, want 409", got)
	}
	if got := statusPost(t, app, "/api/v1/sim/gates/GATE-IN-01/pay", `{"method":"CASH"}`); got != 409 {
		t.Fatalf("bayar di gerbang masuk: status = %d, want 409", got)
	}
}

// Health kini melaporkan status per gate_code, bukan "entry"/"exit" tunggal.
func TestHealthMelaporkanPerGateCode(t *testing.T) {
	app, _ := testApp(t)

	m := getJSON(t, app, "/api/v1/health")
	gates, ok := m["gates"].(map[string]any)
	if !ok {
		t.Fatalf("gates = %v, want objek", m["gates"])
	}
	for _, code := range []string{"GATE-IN-01", "GATE-OUT-01"} {
		g, ada := gates[code].(map[string]any)
		if !ada {
			t.Fatalf("health tak memuat %s: %v", code, gates)
		}
		if g["state"] == nil || g["kind"] == nil {
			t.Fatalf("%s tanpa state/kind: %v", code, g)
		}
	}
}

// Endpoint lama harus tetap hidup selama dashboard belum bermigrasi.
func TestEndpointLamaMasihBerfungsi(t *testing.T) {
	app, _ := testApp(t)

	m := post(t, app, "/api/v1/sim/entry/loop", `{"loop":"pre","high":true}`)
	if m["entry_state"] == nil {
		t.Fatalf("balasan lama kehilangan entry_state: %v", m)
	}
	d := getJSON(t, app, "/api/v1/devices")
	if gates, ok := d["gates"].([]any); !ok || len(gates) != 2 {
		t.Fatalf("/api/v1/devices = %v", d["gates"])
	}
}

// Endpoint kesehatan per gerbang (task 3.4).
func TestEndpointKesehatanPerGerbang(t *testing.T) {
	app, _ := testApp(t)

	h := getJSON(t, app, "/api/v1/gates/GATE-IN-01/health")
	if h["gate_code"] != "GATE-IN-01" {
		t.Fatalf("gate_code = %v", h["gate_code"])
	}
	if h["status"] != "ok" {
		t.Fatalf("gerbang simulator sehat: status = %v (%v)", h["status"], h)
	}
	if h["menjawab"] != true {
		t.Fatalf("gerbang harus menjawab: %v", h)
	}
	if h["state"] == nil {
		t.Fatalf("kesehatan tanpa state: %v", h)
	}

	if got := getStatus(t, app, "/api/v1/gates/GATE-HANTU/health"); got != 404 {
		t.Fatalf("gerbang tak dikenal: status = %d, want 404", got)
	}
}

// Health lahan menyertakan kesehatan tiap gerbang + rollup terpisah.
//
// `status` tetap soal proses edge-api; kesehatan lahan dibaca dari `gates_status`.
// Menggabungkan keduanya membuat pemantau me-restart edge-api saat satu controller
// tercabut — yang justru menjatuhkan gerbang lain yang masih sehat (P8).
func TestHealthMenyertakanKesehatanGerbang(t *testing.T) {
	app, _ := testApp(t)

	m := getJSON(t, app, "/api/v1/health")
	if m["status"] != "ok" {
		t.Fatalf("status proses = %v, want ok", m["status"])
	}
	if m["gates_status"] != "ok" {
		t.Fatalf("gates_status = %v, want ok", m["gates_status"])
	}

	gates, ok := m["gates"].(map[string]any)
	if !ok {
		t.Fatalf("gates = %v", m["gates"])
	}
	g, ada := gates["GATE-IN-01"].(map[string]any)
	if !ada {
		t.Fatalf("health tak memuat GATE-IN-01: %v", gates)
	}
	// Kontrak lama harus tetap utuh — dashboard yang ada membacanya.
	if g["state"] == nil || g["kind"] == nil {
		t.Fatalf("kontrak lama state/kind hilang: %v", g)
	}
	kes, ok := g["health"].(map[string]any)
	if !ok {
		t.Fatalf("gerbang tanpa health: %v", g)
	}
	if kes["status"] != "ok" || kes["gate_code"] != "GATE-IN-01" {
		t.Fatalf("health gerbang = %v", kes)
	}
}
