package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

func mkReq(method, path, body string) *http.Request {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func readJSON(r *http.Response) map[string]any {
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

const whSecret = "test-webhook-secret"

func payApp(t *testing.T) *fiber.App {
	t.Helper()
	iss := auth.NewIssuer("access-secret-min-32-chars-aaaaaaaa", "refresh-secret-min-32-chars-bbbbb", time.Minute, time.Hour)
	st := store.New()
	seed(st)
	app := fiber.New()
	registerRoutes(app, st, iss)
	registerPaymentRoutes(app, st, whSecret)
	return app
}

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(whSecret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func TestQrisMintAndWebhookSettle(t *testing.T) {
	app := payApp(t)

	// Mint QR.
	code, m := do(t, app, "POST", "/internal/v1/payment/qris", "", `{"tenant_id":"t-jabar","amount":15000,"provider":"midtrans"}`)
	if code != 200 {
		t.Fatalf("mint: %d", code)
	}
	orderID := m["order_id"].(string)
	if orderID == "" || m["qr_string"] == "" {
		t.Fatalf("order/qr kosong: %v", m)
	}

	// Webhook dengan signature valid → settle.
	body := `{"order_id":"` + orderID + `","status":"settlement"}`
	req := postSigned(app, "/webhook/payment/midtrans", body, sign(body))
	if req.status != 200 || req.body["duplicate"] == true {
		t.Fatalf("webhook settle: %d %v", req.status, req.body)
	}

	// Webhook duplikat (PG kirim ulang) → idempoten, tidak double.
	dup := postSigned(app, "/webhook/payment/midtrans", body, sign(body))
	if dup.status != 200 || dup.body["duplicate"] != true {
		t.Fatalf("webhook duplikat harus idempoten: %d %v", dup.status, dup.body)
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	app := payApp(t)
	_, m := do(t, app, "POST", "/internal/v1/payment/qris", "", `{"tenant_id":"t-jabar","amount":5000}`)
	body := `{"order_id":"` + m["order_id"].(string) + `","status":"settlement"}`
	// Signature salah → 401.
	bad := postSigned(app, "/webhook/payment/midtrans", body, "deadbeef")
	if bad.status != 401 {
		t.Fatalf("signature salah harus 401, got %d", bad.status)
	}
}

type resp struct {
	status int
	body   map[string]any
}

func postSigned(app *fiber.App, path, body, sig string) resp {
	req := mkReq("POST", path, body)
	req.Header.Set("X-Signature", sig)
	r, _ := app.Test(req, 2000)
	return resp{status: r.StatusCode, body: readJSON(r)}
}
