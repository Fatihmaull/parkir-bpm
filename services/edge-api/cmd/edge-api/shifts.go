package main

import (
	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
)

// registerShiftRoutes memasang API rekonsiliasi shift (§6.4/§12.6, task 7.4). Bentuknya
// mengikuti PRD §11.2 persis: GET /api/v1/shifts | POST /open | POST /{id}/close.
func registerShiftRoutes(app *fiber.App, svc *gatesvc.Service) {
	st := svc.Store()

	app.Get("/api/v1/shifts", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"shifts": st.ShiftViews()})
	})

	app.Post("/api/v1/shifts/open", func(c *fiber.Ctx) error {
		var b struct {
			OperatorID   string `json:"operator_id"`
			OpeningFloat int64  `json:"opening_float"`
		}
		if err := c.BodyParser(&b); err != nil || b.OperatorID == "" {
			return problemDash(c, fiber.StatusBadRequest, "operator_id wajib diisi")
		}
		id, err := st.OpenShift(c.Context(), b.OperatorID, b.OpeningFloat)
		if err != nil {
			return problemDash(c, fiber.StatusConflict, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true, "id": id})
	})

	app.Post("/api/v1/shifts/:id/close", func(c *fiber.Ctx) error {
		var b struct {
			DeclaredCash int64  `json:"declared_cash"`
			Note         string `json:"note"`
		}
		if err := c.BodyParser(&b); err != nil {
			return problemDash(c, fiber.StatusBadRequest, "body tak valid")
		}
		report, err := st.CloseShift(c.Context(), c.Params("id"), b.DeclaredCash, b.Note)
		if err != nil {
			// Selisih tanpa catatan & shift tak ditemukan/sudah tertutup sama-sama kesalahan
			// permintaan pemanggil (bukan kegagalan server) — 422 lebih tepat daripada 500.
			return problemDash(c, fiber.StatusUnprocessableEntity, err.Error())
		}
		return c.JSON(report)
	})
}
