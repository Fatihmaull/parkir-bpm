package main

import (
	"encoding/csv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
)

// registerDashboardRoutes memasang endpoint baca/tulis untuk halaman dashboard (§12).
func registerDashboardRoutes(app *fiber.App, cfg *config.Config, svc *gatesvc.Service) {
	st := svc.Store()

	// Catatan Keuangan (§12.2) + Volume (§12.3).
	app.Get("/api/v1/transactions", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"transactions": st.TransactionViews(), "payments": st.PaymentViews()})
	})

	// Mapping Slot (§12.4): kapasitas statis vs terisi (live).
	app.Get("/api/v1/slots", func(c *fiber.Ctx) error {
		occ := st.OccupancyByType()
		cap := map[string]int{"mobil": 200, "motor": 300, "truk": 20, "bus": 10}
		rows := []fiber.Map{}
		for _, t := range []string{"mobil", "motor", "truk", "bus"} {
			rows = append(rows, fiber.Map{"vehicle_type": t, "capacity": cap[t], "occupied": occ[t]})
		}
		return c.JSON(fiber.Map{"slots": rows})
	})

	// Konfigurasi Lahan (§12.5) — read-only ringkas untuk demo.
	app.Get("/api/v1/config", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"node_id": cfg.NodeID, "timezone": "Asia/Jakarta",
			"grace_minutes": 15, "max_daily_rate": 30000, "lost_ticket_penalty": 20000,
			"antipassback_reset_hours": 18,
			"tariffs": []fiber.Map{
				{"vehicle_type": "mobil", "base_rate": 5000},
				{"vehicle_type": "motor", "base_rate": 2000},
			},
		})
	})

	// Hardware Config (§12.11): gerbang + transport + status.
	// Daftarnya kini datang dari registry gerbang, bukan dua baris hardcoded, sehingga
	// lahan dengan N gerbang tampil seluruhnya (task 2.1).
	app.Get("/api/v1/devices", func(c *fiber.Ctx) error {
		specs := svc.Specs()
		gates := make([]fiber.Map, 0, len(specs))
		for _, s := range specs {
			m := fiber.Map{
				"code": s.Code, "kind": string(s.Kind),
				"transport": s.Transport, "endpoint": s.Endpoint,
			}
			if r, ok := svc.Gate(s.Code); ok {
				m["state"] = r.State()
			}
			gates = append(gates, m)
		}
		return c.JSON(fiber.Map{"gates": gates})
	})

	// RFID Memberships (§12.10) — list, register, bulk CSV (§12.13).
	app.Get("/api/v1/members", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"members": st.MemberViews()})
	})

	app.Post("/api/v1/members", func(c *fiber.Ctx) error {
		var b struct {
			UID         string   `json:"uid"`
			Plates      []string `json:"plates"`
			VehicleType string   `json:"vehicle_type"`
			ValidUntil  string   `json:"valid_until"` // YYYY-MM-DD
		}
		if err := c.BodyParser(&b); err != nil || b.UID == "" {
			return fiber.ErrBadRequest
		}
		vu := parseDate(b.ValidUntil, time.Now().AddDate(1, 0, 0))
		vt := b.VehicleType
		if vt == "" {
			vt = "mobil"
		}
		id := st.AddMember(b.UID, b.Plates, vt, vu)
		return c.JSON(fiber.Map{"ok": true, "id": id})
	})

	app.Get("/api/v1/members/export", func(c *fiber.Ctx) error {
		var sb strings.Builder
		w := csv.NewWriter(&sb)
		_ = w.Write([]string{"rfid_uid", "plates", "vehicle_type", "valid_until", "status", "presence"})
		for _, m := range st.MemberViews() {
			_ = w.Write([]string{m.RfidUID, strings.Join(m.Plates, "|"), m.VehicleType, m.ValidUntil, m.Status, m.Presence})
		}
		w.Flush()
		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", "attachment; filename=memberships.csv")
		return c.SendString(sb.String())
	})

	// Import CSV 3-langkah (§12.13): mode=validate (pratinjau) | apply (transaksional).
	app.Post("/api/v1/members/import", func(c *fiber.Ctx) error {
		mode := c.Query("mode", "validate")
		r := csv.NewReader(strings.NewReader(string(c.Body())))
		records, err := r.ReadAll()
		if err != nil || len(records) < 1 {
			return problemDash(c, 400, "CSV tidak valid")
		}
		type row struct {
			Line   int      `json:"line"`
			UID    string   `json:"uid"`
			Valid  bool     `json:"valid"`
			Reason string   `json:"reason,omitempty"`
			Plates []string `json:"plates"`
			VType  string   `json:"vehicle_type"`
			VUntil string   `json:"valid_until"`
		}
		var rows []row
		allValid := true
		for i, rec := range records {
			if i == 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "rfid_uid") {
				continue // header
			}
			rw := row{Line: i + 1, Valid: true}
			if len(rec) < 4 {
				rw.Valid, rw.Reason = false, "kolom kurang"
			} else {
				rw.UID = strings.TrimSpace(rec[0])
				rw.Plates = strings.Split(rec[1], "|")
				rw.VType = strings.TrimSpace(rec[2])
				rw.VUntil = strings.TrimSpace(rec[3])
				if rw.UID == "" {
					rw.Valid, rw.Reason = false, "rfid_uid kosong"
				}
			}
			if !rw.Valid {
				allValid = false
			}
			rows = append(rows, rw)
		}
		// Transaksional (§12.13): satu baris gagal → seluruh batch dibatalkan.
		if mode == "apply" {
			if !allValid {
				return problemDash(c, 422, "import dibatalkan: ada baris tidak valid")
			}
			for _, rw := range rows {
				st.AddMember(rw.UID, rw.Plates, rw.VType, parseDate(rw.VUntil, time.Now().AddDate(1, 0, 0)))
			}
			return c.JSON(fiber.Map{"applied": len(rows)})
		}
		valid := 0
		for _, rw := range rows {
			if rw.Valid {
				valid++
			}
		}
		return c.JSON(fiber.Map{"rows": rows, "total": len(rows), "valid": valid, "all_valid": allValid})
	})
}

func parseDate(s string, def time.Time) time.Time {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return def
}

func problemDash(c *fiber.Ctx, status int, detail string) error {
	c.Set("Content-Type", "application/problem+json")
	return c.Status(status).JSON(fiber.Map{"status": status, "detail": detail})
}
