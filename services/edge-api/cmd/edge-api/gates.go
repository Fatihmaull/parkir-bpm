package main

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
)

// registerGateRoutes memasang API gerbang yang beralamat gate_code (task 2.1 & 2.2).
//
// Jalur lama "/api/v1/sim/entry/..." dan "/api/v1/sim/exit/..." hanya mengenal satu
// gerbang tiap jenis, sehingga lahan dengan dua gerbang masuk tak punya cara menyentuh
// yang kedua. Di sini setiap gerbang dipanggil dengan code-nya sendiri.
func registerGateRoutes(app *fiber.App, svc *gatesvc.Service) {
	// Daftar gerbang yang dikenal Edge, sesuai urutan sumber konfigurasi.
	app.Get("/api/v1/gates", func(c *fiber.Ctx) error {
		specs := svc.Specs()
		out := make([]fiber.Map, 0, len(specs))
		for _, s := range specs {
			m := fiber.Map{
				"code": s.Code, "kind": string(s.Kind),
				"transport": s.Transport, "endpoint": s.Endpoint,
			}
			if r, ok := svc.Gate(s.Code); ok {
				m["state"] = r.State()
			}
			out = append(out, m)
		}
		return c.JSON(fiber.Map{"gates": out})
	})

	app.Get("/api/v1/gates/:code/state", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"gate_code": r.Spec().Code, "state": r.State()})
	})

	g := app.Group("/api/v1/sim/gates/:code")

	g.Post("/loop", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		var b struct {
			Loop string `json:"loop"` // "pre" (LD1/LD3) | "post" (LD2/LD4)
			High bool   `json:"high"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		// Injeksi sinkron: balasan memuat state SETELAH event diproses (lihat gatesvc).
		if err := r.DriveLoop(b.Loop, b.High); err != nil {
			return petakanError(err)
		}
		return balasState(c, r)
	})

	g.Post("/button", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		if err := r.FireEntry(gate.Event{Kind: gate.EvButton}); err != nil {
			return petakanError(err)
		}
		return balasState(c, r)
	})

	g.Post("/rfid", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		var b struct {
			UID string `json:"uid"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		if err := r.TapRFID(b.UID); err != nil {
			return petakanError(err)
		}
		return balasState(c, r)
	})

	g.Post("/ticket-taken", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		if err := r.TakeTicket(); err != nil {
			return petakanError(err)
		}
		return balasState(c, r)
	})

	g.Post("/identify", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
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
		if err := r.FireExit(ev); err != nil {
			return petakanError(err)
		}
		amount, _ := r.Amount()
		return c.JSON(fiber.Map{
			"ok": true, "gate_code": r.Spec().Code, "state": r.State(), "amount": amount,
		})
	})

	g.Post("/photo", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		var b struct {
			Match bool `json:"match"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		if err := r.FireExit(gate.XEvent{Kind: gate.XEvPhotoVerdict, Match: b.Match}); err != nil {
			return petakanError(err)
		}
		return balasState(c, r)
	})

	g.Post("/pay", func(c *fiber.Ctx) error {
		r, err := cariGate(svc, c)
		if err != nil {
			return err
		}
		var b struct {
			Method   string `json:"method"`
			Tendered int64  `json:"tendered"`
		}
		if err := c.BodyParser(&b); err != nil {
			return fiber.ErrBadRequest
		}
		if err := r.FireExit(gate.XEvent{Kind: gate.XEvPaymentSelect, Method: b.Method, Tendered: b.Tendered}); err != nil {
			return petakanError(err)
		}
		return balasState(c, r)
	})
}

// cariGate mengambil runner gerbang dari parameter :code.
func cariGate(svc *gatesvc.Service, c *fiber.Ctx) (*gatesvc.Runner, error) {
	code := c.Params("code")
	r, ok := svc.Gate(code)
	if !ok {
		return nil, fiber.NewError(fiber.StatusNotFound, "gerbang "+code+" tidak dikenal")
	}
	return r, nil
}

// balasState menjawab dengan state gerbang setelah aksi diproses.
func balasState(c *fiber.Ctx, r *gatesvc.Runner) error {
	return c.JSON(fiber.Map{"ok": true, "gate_code": r.Spec().Code, "state": r.State()})
}

// petakanError menerjemahkan kesalahan domain gerbang menjadi status HTTP.
func petakanError(err error) error {
	switch {
	case errors.Is(err, gatesvc.ErrJenisGerbang):
		// Mis. menekan tombol tiket di gerbang keluar: permintaan yang salah alamat,
		// bukan kegagalan server.
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case errors.Is(err, gatesvc.ErrBerhenti):
		return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
	default:
		return err
	}
}
