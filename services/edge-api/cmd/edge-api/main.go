// edge-api — backend Edge Node. PRD §4.2: seluruh logika transaksi, state machine gerbang,
// fare engine, audit chain, WS server untuk POS. "Boleh mati? Tidak — gerbang berhenti."
//
// Mode saat ini: menjalankan gerbang masuk & keluar di atas SIMULATOR (P7) dengan penyimpanan
// in-memory (memstore, D12). Repository pgx nyata menggantikan memstore saat DB tersedia,
// tanpa mengubah logika (interface identik).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
	"github.com/jabar-creative/parkir/edge-api/internal/cronjobs"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
	"github.com/jabar-creative/parkir/edge-api/internal/lpr"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/pgstore"
	"github.com/jabar-creative/parkir/edge-api/internal/svcnotify"
	"github.com/jabar-creative/parkir/edge-api/internal/syncagent"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// shutdownGrace membatasi lama menutup HTTP dengan rapi.
//
// Sengaja jauh di bawah NFR-2.3 (pulih < 15 dtk): waktu itu harus memuat berhenti DAN
// nyala kembali, jadi separuhnya saja untuk berhenti sudah terlalu banyak.
const shutdownGrace = 5 * time.Second

func main() {
	// Seluruh jalur fatal keluar lewat sini dengan status bukan-nol.
	//
	// Ini syarat mutlak bagi manajer service: `Restart=always` hanya menangkap proses
	// yang MATI. Proses yang tetap hidup setelah kegagalan fatal terbaca "aktif" oleh
	// systemd maupun NSSM, tidak pernah dinyalakan ulang, dan gerbang berhenti melayani
	// tanpa satu pun alarm berbunyi.
	if err := run(); err != nil {
		slog.Error("edge-api berhenti karena kesalahan fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("gagal memuat config: %w", err)
	}

	slog.SetDefault(newLogger(cfg.LogLevel))
	slog.Info("edge-api mulai", "node_id", cfg.NodeID, "env", cfg.Env,
		"gate_in", cfg.GateIn.Transport, "gate_out", cfg.GateOut.Transport)

	hub := wsbus.NewHub()

	// Repository (task 5.1): postgres (pgstore) menggantikan memory (memstore) lewat kontrak
	// yang identik (gatesvc.Store) — tak ada logika gerbang yang berubah, hanya di mana
	// datanya hidup. Seed tarif/member demo di bawah HANYA untuk mode memory (D12): mode
	// postgres membaca data sungguhan dari tabel tariffs/memberships (task 5.2/5.4), bukan
	// data karangan yang muncul lagi tiap restart.
	//
	// gateSource ikut ditentukan di sini (task 2.1): mode postgres memuat gerbang dari
	// tabel `gates`, mode memory tetap dari `.env` (SpecsFromConfig) — tabel `gates` cuma
	// berarti kalau ada repository sungguhan di baliknya.
	var store gatesvc.Store
	var gateSource gatesvc.GateSource
	switch cfg.Store {
	case "postgres":
		pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("gagal membuat pool postgres: %w", err)
		}
		defer pool.Close()
		pgs, err := pgstore.New(context.Background(), pool, cfg.TenantCode, cfg.SiteCode, cfg.NodeID)
		if err != nil {
			return fmt.Errorf("gagal menyiapkan repository postgres: %w", err)
		}
		store = pgs
		gateSource = pgs
		slog.Info("repository: postgres", "tenant_code", cfg.TenantCode, "site_code", cfg.SiteCode)
	default: // "memory" (D12) — juga default aman bila EDGE_STORE tak diset/salah eja
		ms := memstore.New(cfg.NodeID, time.Now)
		// Seed tarif default agar fare engine berfungsi di mode demo.
		ms.SetRate("mobil", gate.RateCard{BaseRate: 5000})
		ms.SetRate("motor", gate.RateCard{BaseRate: 2000})
		// Seed member demo (registrasi RFID §8.1).
		ms.AddMember("04A1B2C3", []string{"D1234ABC"}, "mobil", time.Now().AddDate(1, 0, 0))
		ms.AddMember("04D4E5F6", []string{"D5678XYZ"}, "motor", time.Now().AddDate(0, 6, 0))
		store = ms
		gateSource = gatesvc.SpecsFromConfig(cfg)
		slog.Info("repository: memory (demo/simulator, D12)")
	}

	// Recognizer: mode demo memakai Stub berlabel (BUKAN model nyata; YOLOv8/EasyOCR = Fase 2, §17).
	// Produksi menyuntik klien gRPC ke lpr-svc via LPR_GRPC_ADDR.
	var rec lpr.Recognizer = lpr.Stub{Plate: "D1234ABC", Confidence: 0.91, EngineVersion: "stub-demo-no-model"}
	if cfg.Store == "postgres" { // proxy: mode produksi → jangan palsukan OCR
		rec = lpr.Degraded{EngineVersion: cfg.LPREngineVer}
	}
	svc, err := gatesvc.New(gatesvc.Config{
		NodeID: cfg.NodeID, TenantID: cfg.TenantCode, SiteID: cfg.SiteCode,
		Site:       gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000, LostTicketPenalty: 20000},
		Recognizer: rec, LPRDeadline: cfg.LPRDeadline,
		Source: gateSource,
	}, hub, store)
	if err != nil {
		return fmt.Errorf("konfigurasi gerbang tidak sah: %w", err)
	}
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
		Alert: func(typ, sev, msg string) {
			hub.Publish("alert.raised", map[string]any{"type": typ, "severity": sev, "message": msg})
		},
	}); err != nil {
		return fmt.Errorf("gagal register cron: %w", err)
	}
	c.Start()
	defer c.Stop()

	// Sync agent (§10.2) — jalan hanya bila endpoint Cloud dikonfigurasi. Cloud = tujuan
	// replikasi, bukan dependensi runtime (P1): gerbang tetap jalan walau ini mati.
	syncCtx, syncCancel := context.WithCancel(context.Background())
	defer syncCancel()
	if cfg.SyncEndpoint != "" {
		sink := syncagent.NewHTTPSink(cfg.SyncEndpoint, cfg.TenantCode)
		agent := syncagent.New(store.Outbox(), sink, cfg.SyncBatch)
		go agent.Run(syncCtx, cfg.SyncTick)
		slog.Info("sync agent aktif", "endpoint", cfg.SyncEndpoint, "tick", cfg.SyncTick)
	} else {
		slog.Info("sync agent nonaktif (SYNC_CLOUD_ENDPOINT kosong) — mode offline")
	}

	app := fiber.New(fiber.Config{
		AppName:      "edge-api",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})
	registerRoutes(app, cfg, svc, hub)

	// Soket dibuka SEBELUM melayani, bukan di dalam goroutine.
	//
	// app.Listen menggabungkan "mengikat port" dengan "melayani selamanya", sehingga
	// kegagalan bind — port dipakai proses lain, izin kurang — hanya terlihat sebagai
	// error di dalam goroutine. Dengan memisahkannya, port yang tak bisa diikat menjadi
	// kegagalan startup yang jujur, dan READY=1 baru dikirim setelah soket benar-benar
	// ada. Manajer service karenanya tak pernah mengumumkan "siap" untuk proses yang
	// sebenarnya tak mendengarkan apa pun.
	addr := ":" + strconv.Itoa(cfg.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gagal mengikat %s: %w", addr, err)
	}
	slog.Info("HTTP listen", "addr", addr)

	srvErr := make(chan error, 1)
	go func() { srvErr <- app.Listener(ln) }()

	// Manajer service (systemd) — tanpa-operasi di luar systemd.
	notifier, err := svcnotify.New()
	if err != nil {
		return fmt.Errorf("gagal menyiapkan notifikasi service: %w", err)
	}
	defer func() { _ = notifier.Close() }()

	if err := notifier.Ready(); err != nil {
		return fmt.Errorf("gagal mengumumkan siap: %w", err)
	}
	_ = notifier.Status(fmt.Sprintf("melayani %d gerbang di %s", len(svc.Specs()), addr))

	wdCtx, wdCancel := context.WithCancel(context.Background())
	defer wdCancel()
	go jalankanWatchdog(wdCtx, notifier, svc)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Server yang berhenti sendiri adalah kegagalan fatal, bukan kejadian yang cukup
	// dicatat: tanpa HTTP, POS dan dashboard lahan buta sepenuhnya.
	select {
	case <-quit:
		slog.Info("shutdown diminta")
	case err := <-srvErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("server HTTP berhenti sendiri: %w", err)
		}
		return errors.New("server HTTP berhenti sendiri tanpa diminta")
	}

	_ = notifier.Stopping()
	_ = notifier.Status("berhenti")
	wdCancel()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := app.ShutdownWithContext(ctx); err != nil {
		// Shutdown yang tak tuntas tetap diteruskan ke penutupan gerbang: palang dan
		// controller lebih penting untuk ditutup rapi daripada koneksi HTTP yang tersisa.
		slog.Error("shutdown HTTP tak tuntas", "err", err)
	}
	slog.Info("edge-api berhenti bersih")
	return nil
}

// jalankanWatchdog membuktikan ke manajer service bahwa proses ini masih berjalan.
//
// Yang dibuktikan adalah HIDUPNYA MESIN INTERNAL, bukan sehatnya gerbang. Gerbang yang
// controller-nya tercabut harus terbaca "down" di endpoint kesehatan, tetapi tak boleh
// membuat watchdog membunuh edge-api: restart tak menyambungkan kabel, dan ia menjatuhkan
// gerbang-gerbang lain yang masih melayani (P8). Lihat K26 di docs/CATATAN_KEPUTUSAN.md.
func jalankanWatchdog(ctx context.Context, n *svcnotify.Notifier, svc *gatesvc.Service) {
	jeda := n.WatchdogInterval()
	if jeda <= 0 {
		return // manajer service tak meminta watchdog
	}
	slog.Info("watchdog service aktif", "jeda", jeda)

	t := time.NewTicker(jeda)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !svc.MesinInternalHidup() {
				// Ping SENGAJA ditahan: inilah satu-satunya cara memberi tahu manajer
				// service bahwa proses ini perlu dimatikan dan dinyalakan ulang.
				slog.Error("gelang healthcheck internal berhenti menyapu — watchdog ditahan",
					"sapuan_terakhir", svc.LastHealthSweep())
				continue
			}
			if err := n.Watchdog(); err != nil {
				slog.Warn("gagal mengirim watchdog", "err", err)
			}
		}
	}
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
