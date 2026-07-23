// cloud-api — backend Cloud. PRD §4.2: terima sync, agregasi, serve dashboard, verifikasi audit chain.
// PRD §4.3: cache in-process (ristretto) + rate limit in-memory → cloud-api HARUS single instance
// sampai Redis dianggarkan.
//
// Scaffold Minggu 1: config env, logging, health. Auth/sync/dashboard API menyusul per timeline.
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
)

func main() {
	port := envInt("CLOUD_API_PORT", 9090)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if os.Getenv("CLOUD_DATABASE_URL") == "" {
		slog.Warn("CLOUD_DATABASE_URL kosong — jalankan hanya untuk cek kesehatan")
	}

	app := fiber.New(fiber.Config{AppName: "cloud-api"})
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "cloud-api"})
	})

	// TODO(Minggu 1): auth (JWT/Argon2id), CRUD sites/tariffs.
	// TODO(Minggu 4): /internal/v1/sync/batch receiver + idempotensi, /webhook/payment/{provider}.

	go func() {
		addr := ":" + strconv.Itoa(port)
		slog.Info("cloud-api listen", "addr", addr)
		if err := app.Listen(addr); err != nil {
			slog.Error("server berhenti", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = app.ShutdownWithContext(ctx)
	slog.Info("cloud-api berhenti bersih")
}

func envInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
