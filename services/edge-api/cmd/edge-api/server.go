package main

import (
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/config"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// registerRoutes memasang API Edge (PRD §13.1) + endpoint simulator untuk Field Monitor (§12.8).
func registerRoutes(app *fiber.App, cfg *config.Config, svc *gatesvc.Service, hub *wsbus.Hub) {
	// Health (§13.1): status device, sync, chain.
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		entries := svc.Store().AuditEntries()
		_, chainOK := audit.Verify(entries)
		return c.JSON(fiber.Map{
			"status":  "ok",
			"node_id": cfg.NodeID,
			"env":     cfg.Env,
			"gates": fiber.Map{
				"entry": string(svc.EntryState()),
				"exit":  string(svc.ExitState()),
			},
			"ws_subscribers": hub.Count(),
			"sync":           fiber.Map{"state": "outbox", "pending": svc.Store().Outbox().PendingCount()},
			"chain":          fiber.Map{"verified": chainOK, "entries": len(entries)},
		})
	})

	app.Get("/api/v1/gate/entry/state", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"state": string(svc.EntryState())})
	})
	app.Get("/api/v1/gate/exit/state", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"state": string(svc.ExitState())})
	})

	// ── WebSocket stream (§13.1) ──
	app.Use("/api/v1/stream", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/api/v1/stream", websocket.New(func(conn *websocket.Conn) {
		ch, unsub := hub.Subscribe()
		defer unsub()
		for ev := range ch {
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}
	}))

	// Daftar kendaraan aktif (IN_PREMISES) — untuk POS memilih transaksi & metrik.
	app.Get("/api/v1/active", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"active": svc.Store().ActiveVehicles()})
	})

	// ── Field Monitor / mode simulator (§12.8) ──
	sim := app.Group("/api/v1/sim")

	sim.Post("/entry/loop", func(c *fiber.Ctx) error {
		var b struct {
			Loop string `json:"loop"` // "pre" (LD1) | "post" (LD2)
			High bool   `json:"high"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		if b.Loop == "post" {
			svc.EntrySim().LoopPost.Drive(b.High)
		} else {
			svc.EntrySim().LoopPre.Drive(b.High)
		}
		return c.JSON(fiber.Map{"ok": true, "entry_state": string(svc.EntryState())})
	})
	sim.Post("/entry/button", func(c *fiber.Ctx) error {
		svc.FireEntry(gate.Event{Kind: gate.EvButton})
		return c.JSON(fiber.Map{"ok": true, "entry_state": string(svc.EntryState())})
	})
	sim.Post("/entry/rfid", func(c *fiber.Ctx) error {
		var b struct {
			UID string `json:"uid"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		svc.EntrySim().RFID.SimTap(b.UID)
		return c.JSON(fiber.Map{"ok": true, "entry_state": string(svc.EntryState())})
	})
	sim.Post("/entry/ticket-taken", func(c *fiber.Ctx) error {
		svc.EntrySim().Printer.SimTake()
		return c.JSON(fiber.Map{"ok": true, "entry_state": string(svc.EntryState())})
	})

	sim.Post("/exit/loop", func(c *fiber.Ctx) error {
		var b struct {
			Loop string `json:"loop"` // "pre" (LD3) | "post" (LD4)
			High bool   `json:"high"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		if b.Loop == "post" {
			svc.ExitSim().LoopPost.Drive(b.High)
		} else {
			svc.ExitSim().LoopPre.Drive(b.High)
		}
		return c.JSON(fiber.Map{"ok": true, "exit_state": string(svc.ExitState())})
	})
	sim.Post("/exit/identify", func(c *fiber.Ctx) error {
		var b struct {
			Ticket string `json:"ticket"`
			UID    string `json:"uid"`
			Plate  string `json:"plate"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		switch {
		case b.Ticket != "":
			svc.FireExit(gate.XEvent{Kind: gate.XEvIdentifyTicket, Ticket: b.Ticket})
		case b.UID != "":
			svc.FireExit(gate.XEvent{Kind: gate.XEvIdentifyRFID, UID: b.UID})
		case b.Plate != "":
			svc.FireExit(gate.XEvent{Kind: gate.XEvIdentifyPlate, Plate: b.Plate})
		}
		return c.JSON(fiber.Map{"ok": true, "exit_state": string(svc.ExitState()), "amount": svc.ExitAmount()})
	})
	sim.Post("/exit/photo", func(c *fiber.Ctx) error {
		var b struct {
			Match bool `json:"match"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		svc.FireExit(gate.XEvent{Kind: gate.XEvPhotoVerdict, Match: b.Match})
		return c.JSON(fiber.Map{"ok": true, "exit_state": string(svc.ExitState())})
	})
	sim.Post("/exit/pay", func(c *fiber.Ctx) error {
		var b struct {
			Method   string `json:"method"`
			Tendered int64  `json:"tendered"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		svc.FireExit(gate.XEvent{Kind: gate.XEvPaymentSelect, Method: b.Method, Tendered: b.Tendered})
		return c.JSON(fiber.Map{"ok": true, "exit_state": string(svc.ExitState())})
	})
}
