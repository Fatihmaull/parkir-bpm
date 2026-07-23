package main

import (
	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
)

// registerHealth memasang GET /api/v1/health (PRD §13.1): status device, sync, chain.
// Nilai masih stub sampai subsistem terkait terpasang.
func registerHealth(app *fiber.App, cfg *config.Config) {
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"node_id": cfg.NodeID,
			"env":     cfg.Env,
			"devices": fiber.Map{
				"gate_in":  fiber.Map{"transport": cfg.GateIn.Transport, "status": "unknown"},
				"gate_out": fiber.Map{"transport": cfg.GateOut.Transport, "status": "unknown"},
				"edc":      fiber.Map{"transport": cfg.EDC.Transport, "status": "unknown"},
			},
			"sync":  fiber.Map{"state": "unknown", "pending": 0},
			"chain": fiber.Map{"state": "unknown"},
		})
	})
}
