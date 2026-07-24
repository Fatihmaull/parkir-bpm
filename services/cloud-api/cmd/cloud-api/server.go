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
		var b struct{ RefreshToken string `json:"refresh_token"` }
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

	api.Get("/audit", auth.RequireRole(auth.RoleSuperAdmin, auth.RoleAuditor), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"audit": []any{}, "note": "verifikasi rantai penuh via node; ekspor CSV menyusul"})
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
}

func problem(c *fiber.Ctx, status int, detail string) error {
	c.Set("Content-Type", "application/problem+json")
	return c.Status(status).JSON(fiber.Map{"status": status, "detail": detail})
}
