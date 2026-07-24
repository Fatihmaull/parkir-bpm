// edge-api — backend Edge Node. PRD §4.2: seluruh logika transaksi, state machine gerbang,
// fare engine, audit chain, WS server untuk POS. "Boleh mati? Tidak — gerbang berhenti."
//
// Mode saat ini: menjalankan gerbang masuk & keluar di atas SIMULATOR (P7) dengan penyimpanan
// in-memory (memstore, D12). Repository pgx nyata menggantikan memstore saat DB tersedia,
// tanpa mengubah logika (interface identik).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/robfig/cron/v3"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
	"github.com/jabar-creative/parkir/edge-api/internal/cronjobs"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("gagal memuat config", "err", err)
		os.Exit(1)
	}

	slog.SetDefault(newLogger(cfg.LogLevel))
	slog.Info("edge-api mulai", "node_id", cfg.NodeID, "env", cfg.Env,
		"gate_in", cfg.GateIn.Transport, "gate_out", cfg.GateOut.Transport)

	hub := wsbus.NewHub()
	store := memstore.New(cfg.NodeID, time.Now)
	// Seed tarif default agar fare engine berfungsi di mode demo.
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})
	store.SetRate("motor", gate.RateCard{BaseRate: 2000})

	svc := gatesvc.New(gatesvc.Config{
		NodeID: cfg.NodeID, TenantID: cfg.TenantCode, SiteID: cfg.SiteCode,
		Site: gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000, LostTicketPenalty: 20000},
	}, hub, store)
	svc.Start()
	defer svc.Close()

	// CRON 6 job (§8.3) — idempoten, dijadwalkan robfig/cron.
	c := cron.New()
	if err := cronjobs.Register(c, &cronjobs.Deps{
		Now:              time.Now,
		ResetHours:       18,
		ExpireMembership: store.ExpireMemberships,
		ResetPresence:    store.ResetStalePresence,
		RetryOutbox:      store.Outbox().RequeueFailed,
		VerifyAudit:      store.VerifyChain,
		Alert:            func(typ, sev, msg string) { hub.Publish("alert.raised", map[string]any{"type": typ, "severity": sev, "message": msg}) },
	}); err != nil {
		slog.Error("gagal register cron", "err", err)
	}
	c.Start()
	defer c.Stop()

	app := fiber.New(fiber.Config{
		AppName:      "edge-api",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})
	registerRoutes(app, cfg, svc, hub)

	go func() {
		addr := ":" + strconv.Itoa(cfg.HTTPPort)
		slog.Info("HTTP listen", "addr", addr)
		if err := app.Listen(addr); err != nil {
			slog.Error("server berhenti", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutdown diminta")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("edge-api berhenti bersih")
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
