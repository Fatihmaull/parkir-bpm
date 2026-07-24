// cloud-api — backend Cloud. PRD §4.2: terima sync, agregasi, serve dashboard, verifikasi audit.
// PRD §4.3: cache in-process + rate limit in-memory → cloud-api HARUS single instance sampai Redis.
//
// Mode saat ini: penyimpanan agregat in-memory (store), tanpa Managed PostgreSQL. Auth & isolasi
// multi-tenant sudah nyata. Repository pgx menggantikan store lewat kontrak yang sama.
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

	"github.com/jabar-creative/parkir/cloud-api/internal/auth"
	"github.com/jabar-creative/parkir/cloud-api/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	port := envInt("CLOUD_API_PORT", 9090)

	iss := auth.NewIssuer(
		env("JWT_ACCESS_SECRET", "dev-access-secret-min-32-chars-aaaa"),
		env("JWT_REFRESH_SECRET", "dev-refresh-secret-min-32-chars-bbb"),
		15*time.Minute, 7*24*time.Hour,
	)

	st := store.New()
	seed(st)

	app := fiber.New(fiber.Config{AppName: "cloud-api"})
	registerRoutes(app, st, iss)
	registerPaymentRoutes(app, st, env("PG_WEBHOOK_SIGNATURE_SECRET", "dev-webhook-secret"))

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

// seed mengisi data demo: satu tenant + admin + dua site. Tenant kedua untuk uji isolasi.
func seed(st *store.Store) {
	// Password demo: "admin12345" (di produksi via secret manager, bukan hardcoded).
	hash, _ := auth.HashPassword("admin12345", auth.DefaultParams())
	st.AddUser(&store.User{
		ID: "u-1", TenantID: "t-jabar", Email: "admin@parkir.local",
		PasswordHash: hash, FullName: "Admin Jabar", Role: auth.RoleSuperAdmin,
	})
	st.AddSite(store.Site{ID: "s-1", TenantID: "t-jabar", Code: "mall_jabar", Name: "Mall Jabar", City: "Bandung"})
	st.AddSite(store.Site{ID: "s-2", TenantID: "t-jabar", Code: "plaza_jabar", Name: "Plaza Jabar", City: "Bandung"})
	// Tenant lain — tidak boleh terlihat oleh admin t-jabar (uji isolasi §12.14).
	st.AddSite(store.Site{ID: "s-9", TenantID: "t-other", Code: "other", Name: "Site Tenant Lain", City: "Jakarta"})
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
