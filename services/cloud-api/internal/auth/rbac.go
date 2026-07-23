package auth

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Role — peran pengguna (PRD §12.0 / §14).
type Role string

const (
	RoleSuperAdmin Role = "SuperAdmin"
	RoleAuditor    Role = "Auditor"
	RoleKasir      Role = "Kasir"
)

func (r Role) Valid() bool {
	switch r {
	case RoleSuperAdmin, RoleAuditor, RoleKasir:
		return true
	}
	return false
}

const claimsKey = "auth_claims"

// Middleware memverifikasi Bearer access token dan menaruh Claims ke context.
// RBAC di-enforce di backend (PRD §12.0: "Frontend saja tidak pernah cukup").
func Middleware(iss *Issuer) fiber.Handler {
	return func(c *fiber.Ctx) error {
		h := c.Get("Authorization")
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || token == "" {
			return problem(c, fiber.StatusUnauthorized, "token tidak ada")
		}
		claims, err := iss.Verify(AccessToken, token)
		if err != nil {
			return problem(c, fiber.StatusUnauthorized, "token tidak valid")
		}
		c.Locals(claimsKey, claims)
		return c.Next()
	}
}

// RequireRole membatasi endpoint ke peran tertentu.
func RequireRole(roles ...Role) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims := ClaimsFrom(c)
		if claims == nil {
			return problem(c, fiber.StatusUnauthorized, "belum terautentikasi")
		}
		if !slices.Contains(roles, claims.Role) {
			return problem(c, fiber.StatusForbidden, "peran tidak diizinkan")
		}
		return c.Next()
	}
}

// ClaimsFrom mengambil Claims dari context (nil bila tidak ada).
func ClaimsFrom(c *fiber.Ctx) *Claims {
	if v, ok := c.Locals(claimsKey).(*Claims); ok {
		return v
	}
	return nil
}

// CanAccessSite mengecek apakah claims boleh mengakses site tertentu (site_scope, PRD §12.14).
// SuperAdmin dengan scope kosong → semua site tenant.
func (c *Claims) CanAccessSite(siteID string) bool {
	if len(c.SiteScope) == 0 {
		return true // kosong = semua site dalam tenant
	}
	return slices.Contains(c.SiteScope, siteID)
}

// problem membalas error RFC 7807 (application/problem+json, PRD §13 konvensi).
func problem(c *fiber.Ctx, status int, detail string) error {
	c.Set("Content-Type", "application/problem+json")
	return c.Status(status).JSON(fiber.Map{
		"type":   "about:blank",
		"title":  fiber.NewError(status).Message,
		"status": status,
		"detail": detail,
	})
}
