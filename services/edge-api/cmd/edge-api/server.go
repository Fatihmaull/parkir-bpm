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

		// Status per gate_code — lahan boleh punya lebih dari satu gerbang masuk.
		//
		// Kesehatan tiap gerbang ikut disertakan (task 3.4) supaya satu permintaan cukup
		// untuk halaman status lahan; rinciannya ada di /api/v1/gates/:code/health.
		kes := svc.Health(0)
		gates := fiber.Map{}
		for _, h := range kes.Gates {
			gates[h.Code] = fiber.Map{
				"kind": h.Kind, "state": h.State, "health": h,
			}
		}
		return c.JSON(fiber.Map{
			// status = proses edge-api hidup dan melayani. Ia sengaja TIDAK ikut memburuk
			// saat sebuah gerbang mati: pemantau yang me-restart edge-api karena satu
			// controller tercabut justru menjatuhkan gerbang-gerbang lain yang masih sehat
			// (P8). Kesehatan lahan dibaca dari gates_status.
			"status":         "ok",
			"gates_status":   kes.Status,
			"node_id":        cfg.NodeID,
			"env":            cfg.Env,
			"gates":          gates,
			"ws_subscribers": hub.Count(),
			"sync":           fiber.Map{"state": "outbox", "pending": svc.Store().Outbox().PendingCount()},
			"chain":          fiber.Map{"verified": chainOK, "entries": len(entries)},
			"ocr":            fiber.Map{"count": len(svc.Store().OCRLogs())},
		})
	})

	// Log OCR (§7.2, FR-7.7) — analitik akurasi LPR.
	app.Get("/api/v1/ocr-logs", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ocr_logs": svc.Store().OCRLogs()})
	})

	app.Get("/api/v1/gate/entry/state", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"state": stateGerbang(svc.EntryGate())})
	})
	app.Get("/api/v1/gate/exit/state", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"state": stateGerbang(svc.ExitGate())})
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

	registerDashboardRoutes(app, cfg, svc)
	registerGateRoutes(app, svc)
	registerShiftRoutes(app, svc)

	// ── Field Monitor / mode simulator (§12.8) ──
	//
	// Jalur di bawah ini masih beralamat "entry"/"exit" tunggal dan hanya menyentuh
	// gerbang PERTAMA tiap jenis. Ia dipertahankan supaya dashboard yang sudah ada tetap
	// jalan; penggantinya yang beralamat gate_code ada di registerGateRoutes.
	sim := app.Group("/api/v1/sim")

	sim.Post("/entry/loop", func(c *fiber.Ctx) error {
		var b struct {
			Loop string `json:"loop"` // "pre" (LD1) | "post" (LD2)
			High bool   `json:"high"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		// Injeksi sinkron: balasan harus memuat state SETELAH event diproses (lihat gatesvc).
		r := svc.EntryGate()
		if err := aksiGerbang(r, func() error { return r.DriveLoop(b.Loop, b.High) }); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "entry_state": stateGerbang(r)})
	})
	sim.Post("/entry/button", func(c *fiber.Ctx) error {
		r := svc.EntryGate()
		if err := aksiGerbang(r, func() error { return r.FireEntry(gate.Event{Kind: gate.EvButton}) }); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "entry_state": stateGerbang(r)})
	})
	sim.Post("/entry/rfid", func(c *fiber.Ctx) error {
		var b struct {
			UID string `json:"uid"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		r := svc.EntryGate()
		if err := aksiGerbang(r, func() error { return r.TapRFID(b.UID) }); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "entry_state": stateGerbang(r)})
	})
	sim.Post("/entry/ticket-taken", func(c *fiber.Ctx) error {
		r := svc.EntryGate()
		if err := aksiGerbang(r, r.TakeTicket); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "entry_state": stateGerbang(r)})
	})

	sim.Post("/exit/loop", func(c *fiber.Ctx) error {
		var b struct {
			Loop string `json:"loop"` // "pre" (LD3) | "post" (LD4)
			High bool   `json:"high"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		// Injeksi sinkron: POS mengirim identify tepat setelah LD3 — state harus sudah bergerak.
		r := svc.ExitGate()
		if err := aksiGerbang(r, func() error { return r.DriveLoop(b.Loop, b.High) }); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "exit_state": stateGerbang(r)})
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
		var ev gate.XEvent
		switch {
		case b.Ticket != "":
			ev = gate.XEvent{Kind: gate.XEvIdentifyTicket, Ticket: b.Ticket}
		case b.UID != "":
			ev = gate.XEvent{Kind: gate.XEvIdentifyRFID, UID: b.UID}
		case b.Plate != "":
			ev = gate.XEvent{Kind: gate.XEvIdentifyPlate, Plate: b.Plate}
		default:
			return fiber.ErrBadRequest
		}
		r := svc.ExitGate()
		if err := aksiGerbang(r, func() error { return r.FireExit(ev) }); err != nil {
			return err
		}
		amount, _ := r.Amount()
		return c.JSON(fiber.Map{"ok": true, "exit_state": stateGerbang(r), "amount": amount})
	})
	sim.Post("/exit/photo", func(c *fiber.Ctx) error {
		var b struct {
			Match bool `json:"match"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		r := svc.ExitGate()
		if err := aksiGerbang(r, func() error {
			return r.FireExit(gate.XEvent{Kind: gate.XEvPhotoVerdict, Match: b.Match})
		}); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "exit_state": stateGerbang(r)})
	})
	sim.Post("/exit/pay", func(c *fiber.Ctx) error {
		var b struct {
			Method   string `json:"method"`
			Tendered int64  `json:"tendered"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		r := svc.ExitGate()
		if err := aksiGerbang(r, func() error {
			return r.FireExit(gate.XEvent{Kind: gate.XEvPaymentSelect, Method: b.Method, Tendered: b.Tendered})
		}); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "exit_state": stateGerbang(r)})
	})
}

// stateGerbang membaca state satu gerbang, aman bila gerbangnya tak ada.
func stateGerbang(r *gatesvc.Runner) string {
	if r == nil {
		return ""
	}
	return r.State()
}

// aksiGerbang menjalankan aksi pada gerbang, menolak bila lahan tak punya gerbang itu.
func aksiGerbang(r *gatesvc.Runner, f func() error) error {
	if r == nil {
		return fiber.NewError(fiber.StatusNotFound, "lahan tidak punya gerbang jenis ini")
	}
	if err := f(); err != nil {
		return petakanError(err)
	}
	return nil
}
