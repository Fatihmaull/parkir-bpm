package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/cloud-api/internal/store"
)

// registerPaymentRoutes memasang QRIS/e-wallet (§6.3, FR-7.4/7.5). Kredensial PG HANYA di Cloud (D7).
func registerPaymentRoutes(app *fiber.App, st *store.Store, webhookSecret string) {
	// Mint QR — dipanggil Edge (via internal). Produksi memanggil Midtrans/Xendit;
	// di sini QR string disimulasikan. Edge TAK PERNAH memegang kredensial PG.
	app.Post("/internal/v1/payment/qris", func(c *fiber.Ctx) error {
		var b struct {
			TenantID string `json:"tenant_id"`
			Amount   int64  `json:"amount"`
			Provider string `json:"provider"`
		}
		if err := c.BodyParser(&b); err != nil || b.Amount <= 0 || b.TenantID == "" {
			return fiber.ErrBadRequest
		}
		if b.Provider == "" {
			b.Provider = "midtrans"
		}
		o := st.CreateOrder(b.Provider, b.TenantID, b.Amount, "")
		// QR string dummy deterministik dari order id.
		o.QRString = "00020101021226" + o.OrderID
		return c.JSON(fiber.Map{
			"order_id": o.OrderID, "qr_string": o.QRString, "amount": o.Amount,
			"expiry_seconds": 300,
		})
	})

	// Webhook PG (§6.3): verifikasi signature WAJIB; idempoten by order_id.
	app.Post("/webhook/payment/:provider", func(c *fiber.Ctx) error {
		body := c.Body()
		sig := c.Get("X-Signature")
		if !validSignature(body, sig, webhookSecret) {
			// Signature tidak valid → 401 + (di produksi) audit critical.
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "signature tidak valid"})
		}
		var b struct {
			OrderID string `json:"order_id"`
			Status  string `json:"status"`
		}
		if err := c.BodyParser(&b); err != nil || b.OrderID == "" {
			return fiber.ErrBadRequest
		}
		if b.Status != "" && b.Status != "settlement" && b.Status != "capture" && b.Status != "paid" {
			return c.JSON(fiber.Map{"ok": true, "ignored": b.Status})
		}
		found, dup := st.SettleOrder(b.OrderID)
		if !found {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "order tidak dikenal"})
		}
		// Idempoten: webhook duplikat tetap 200 tapi tidak menyelesaikan ulang.
		return c.JSON(fiber.Map{"ok": true, "duplicate": dup})
	})
}

// validSignature memverifikasi HMAC-SHA256(body, secret) == X-Signature (hex), constant-time.
func validSignature(body []byte, sigHex, secret string) bool {
	if sigHex == "" || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}
