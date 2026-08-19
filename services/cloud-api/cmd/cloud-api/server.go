package main

import (
	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/cloud-api/internal/auth"
	"github.com/jabar-creative/parkir/cloud-api/internal/store"
)

// registerRoutes memasang Cloud API (PRD §13.2): auth, baca ter-scope tenant, penerima sync.
func registerRoutes(app *fiber.App, st *store.Store, iss *auth.Issuer) {
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "cloud-api"})
	})

	// ── Auth (§14) ──
	app.Post("/api/v1/auth/login", func(c *fiber.Ctx) error {
		var b struct{ Email, Password string }
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		u, ok := st.UserByEmail(b.Email)
		if !ok || auth.VerifyPassword(b.Password, u.PasswordHash) != nil {
			// Pesan seragam — jangan bocorkan apakah email ada (anti user-enumeration).
			return problem(c, fiber.StatusUnauthorized, "email atau password salah")
		}
		claims := auth.Claims{UserID: u.ID, TenantID: u.TenantID, Role: u.Role, SiteScope: u.SiteScope}
		access, _ := iss.Issue(auth.AccessToken, claims)
		refresh, _ := iss.Issue(auth.RefreshToken, claims)
		return c.JSON(fiber.Map{
			"access_token": access, "refresh_token": refresh,
			"user": fiber.Map{"id": u.ID, "name": u.FullName, "role": u.Role, "tenant_id": u.TenantID},
		})
	})

	app.Post("/api/v1/auth/refresh", func(c *fiber.Ctx) error {
		var b struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		claims, err := iss.Verify(auth.RefreshToken, b.RefreshToken)
		if err != nil {
			return problem(c, fiber.StatusUnauthorized, "refresh token tidak valid")
		}
		access, _ := iss.Issue(auth.AccessToken, *claims)
		return c.JSON(fiber.Map{"access_token": access})
	})

	app.Post("/api/v1/auth/logout", func(c *fiber.Ctx) error {
		// Stateless: klien membuang token. (Revokasi refresh masuk saat store persisten.)
		return c.JSON(fiber.Map{"ok": true})
	})

	// ── Protected + tenant-scoped (§12.14) ──
	api := app.Group("/api/v1", auth.Middleware(iss))

	api.Get("/sites", func(c *fiber.Ctx) error {
		cl := auth.ClaimsFrom(c)
		return c.JSON(fiber.Map{"sites": st.Sites(cl.TenantID, cl.SiteScope)})
	})

	api.Get("/transactions", func(c *fiber.Ctx) error {
		cl := auth.ClaimsFrom(c)
		// tenant_id SELALU dari klaim token, TIDAK PERNAH dari query — cegah lintas-tenant.
		return c.JSON(fiber.Map{"transactions": st.Transactions(cl.TenantID)})
	})

	// Ringkasan kontinuitas tiap rantai node (task 8.5) — bukan dump audit_logs mentah
	// (bisa besar); ?node_id= mengembalikan entri penuh satu node untuk inspeksi/ekspor.
	api.Get("/audit", auth.RequireRole(auth.RoleSuperAdmin, auth.RoleAuditor), func(c *fiber.Ctx) error {
		tid := cl(c).TenantID
		if nodeID := c.Query("node_id"); nodeID != "" {
			return c.JSON(fiber.Map{"node_id": nodeID, "entries": st.AuditEntries(tid, nodeID)})
		}
		return c.JSON(fiber.Map{"chains": st.AuditChains(tid)})
	})

	// Verifikasi rantai audit on-demand (§9.4): re-hash penuh dari genesis, terpisah dari
	// pemeriksaan checkpoint incremental yang sudah terjadi tiap batch masuk (ApplyAuditBatch).
	// Body: {"node_id": "..."} — kosong berarti verifikasi semua node tenant ini.
	api.Post("/audit/verify", auth.RequireRole(auth.RoleSuperAdmin, auth.RoleAuditor), func(c *fiber.Ctx) error {
		tid := cl(c).TenantID
		var b struct {
			NodeID string `json:"node_id"`
		}
		_ = c.BodyParser(&b)
		nodeIDs := []string{b.NodeID}
		if b.NodeID == "" {
			nodeIDs = nil
			for _, ch := range st.AuditChains(tid) {
				nodeIDs = append(nodeIDs, ch.NodeID)
			}
		}
		results := make([]fiber.Map, 0, len(nodeIDs))
		allOK := true
		for _, nid := range nodeIDs {
			brokenSeq, ok, checked := st.VerifyAuditChain(tid, nid)
			if !ok {
				allOK = false
			}
			results = append(results, fiber.Map{"node_id": nid, "verified": ok, "broken_seq": brokenSeq, "checked": checked})
		}
		return c.JSON(fiber.Map{"verified": allOK, "chains": results})
	})

	// Tarif (§13.2) — versioned; POST membuat versi baru (tidak menimpa, D5).
	api.Get("/tariffs", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"tariffs": []fiber.Map{
			{"vehicle_type": "mobil", "base_rate": 5000, "effective_from": "2026-07-01"},
			{"vehicle_type": "motor", "base_rate": 2000, "effective_from": "2026-07-01"},
		}})
	})
	api.Post("/tariffs", auth.RequireRole(auth.RoleSuperAdmin), func(c *fiber.Ctx) error {
		var b map[string]any
		_ = c.BodyParser(&b)
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"ok": true, "note": "versi tarif baru dibuat (audit warning)"})
	})

	// Laporan agregat dari data ter-sync (§13.2). tenant selalu dari klaim.
	api.Get("/reports/financial", func(c *fiber.Ctx) error {
		var total int64
		byStatus := map[string]int{}
		for _, t := range st.Transactions(cl(c).TenantID) {
			if amt, ok := t.Payload["amount"].(float64); ok {
				total += int64(amt)
			}
			if s, ok := t.Payload["status"].(string); ok {
				byStatus[s]++
			}
		}
		return c.JSON(fiber.Map{"total_amount": total, "by_status": byStatus, "count": len(st.Transactions(cl(c).TenantID))})
	})
	api.Get("/reports/occupancy", func(c *fiber.Ctx) error {
		inside := 0
		for _, t := range st.Transactions(cl(c).TenantID) {
			if s, _ := t.Payload["status"].(string); s == "IN_PREMISES" {
				inside++
			}
		}
		return c.JSON(fiber.Map{"inside": inside})
	})

	// ── Internal: penerima sync dari Edge (§10.3). Produksi: mTLS per node. ──
	app.Post("/internal/v1/sync/batch", func(c *fiber.Ctx) error {
		var b struct {
			TenantID string      `json:"tenant_id"`
			Items    []store.Txn `json:"items"`
		}
		if err := c.BodyParser(&b); err != nil || b.TenantID == "" {
			return fiber.ErrBadRequest
		}
		applied, skipped := st.ApplyBatch(b.TenantID, b.Items)
		return c.JSON(fiber.Map{"applied": applied, "skipped": skipped})
	})

	// Penerima sync audit_logs (task 8.5) — TERPISAH dari batch di atas. Tiap entri
	// diverifikasi kriptografis (kontinuitas seq + rantai hash) sebelum diterima; lihat
	// internal/store/audit.go. Produksi: mTLS per node, sama seperti /sync/batch.
	app.Post("/internal/v1/sync/audit-batch", func(c *fiber.Ctx) error {
		var b struct {
			TenantID string           `json:"tenant_id"`
			Entries  []map[string]any `json:"entries"`
		}
		if err := c.BodyParser(&b); err != nil || b.TenantID == "" {
			return fiber.ErrBadRequest
		}
		res := st.ApplyAuditBatch(b.TenantID, b.Entries)
		return c.JSON(res)
	})
}

// cl mengambil klaim token (tenant selalu dari sini, bukan dari query — §12.14).
func cl(c *fiber.Ctx) *auth.Claims { return auth.ClaimsFrom(c) }

func problem(c *fiber.Ctx, status int, detail string) error {
	c.Set("Content-Type", "application/problem+json")
	return c.Status(status).JSON(fiber.Map{"status": status, "detail": detail})
}
