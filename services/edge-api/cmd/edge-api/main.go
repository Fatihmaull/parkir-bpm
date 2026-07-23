// edge-api — backend Edge Node. PRD §4.2: seluruh logika transaksi, state machine gerbang,
// fare engine, audit chain, WS server untuk POS. "Boleh mati? Tidak — gerbang berhenti."
//
// Scaffold Minggu 1 (PRD §16.3): config, structured logging, health endpoint, graceful shutdown.
// State machine, hardware bridge, fare engine, dan sync agent menyusul per timeline.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("gagal memuat config", "err", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	slog.Info("edge-api mulai",
		"node_id", cfg.NodeID, "env", cfg.Env,
		"gate_in", cfg.GateIn.Transport, "gate_out", cfg.GateOut.Transport)

	app := fiber.New(fiber.Config{
		AppName:      "edge-api",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})

	registerHealth(app, cfg)

	// TODO(Minggu 1): repository layer + koneksi pgx pool.
	// TODO(Minggu 2): state machine gerbang, WS /api/v1/stream, LPR gRPC client.
	// TODO(Minggu 3): payment adapter, audit chain, CRON.
	// TODO(Minggu 4): sync agent (outbox → Cloud).

	go func() {
		addr := ":" + itoa(cfg.HTTPPort)
		slog.Info("HTTP listen", "addr", addr)
		if err := app.Listen(addr); err != nil {
			slog.Error("server berhenti", "err", err)
		}
	}()

	// Graceful shutdown (NFR-2.3: pemulihan < 15 detik).
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
	// PRD §14: structured logging, tanpa PII/PAN.
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
