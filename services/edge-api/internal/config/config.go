// Package config memuat konfigurasi edge-api dari environment (.env).
// PRD §14 / NFR-3: nol kredensial hardcoded — seluruhnya via env.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	NodeID     string
	TenantCode string
	SiteCode   string
	Env        string
	LogLevel   string

	HTTPPort    int
	DatabaseURL string

	JWTAccessSecret string
	JWTAccessTTL    time.Duration

	LPRAddr      string
	LPRDeadline  time.Duration
	LPREngineVer string

	GateIn  GateConfig
	GateOut GateConfig
	EDC     TransportConfig

	SyncEndpoint string
	SyncTick     time.Duration
	SyncBatch    int
}

// GateConfig — transport & alamat controller gerbang (PRD §5.1).
type GateConfig struct {
	Transport string // serial|tcp|sim
	Endpoint  string // "COM3" | "10.0.0.5:5001"
	Addr      byte   // 0x01 / 0x02
}

type TransportConfig struct {
	Transport string
	Endpoint  string
}

// Load membaca env dan mengembalikan Config, atau error bila nilai wajib hilang.
func Load() (*Config, error) {
	c := &Config{
		NodeID:          env("NODE_ID", ""),
		TenantCode:      env("TENANT_CODE", ""),
		SiteCode:        env("SITE_CODE", ""),
		Env:             env("EDGE_ENV", "development"),
		LogLevel:        env("EDGE_LOG_LEVEL", "info"),
		HTTPPort:        envInt("EDGE_API_PORT", 8080),
		DatabaseURL:     env("EDGE_DATABASE_URL", ""),
		JWTAccessSecret: env("JWT_ACCESS_SECRET", ""),
		JWTAccessTTL:    envDur("JWT_ACCESS_TTL", 15*time.Minute),
		LPRAddr:         env("LPR_GRPC_ADDR", "localhost:50051"),
		LPRDeadline:     time.Duration(envInt("LPR_DEADLINE_MS", 1000)) * time.Millisecond,
		LPREngineVer:    env("LPR_ENGINE_VERSION", "unknown"),
		GateIn: GateConfig{
			Transport: env("GATE_IN_TRANSPORT", "sim"),
			Endpoint:  env("GATE_IN_ENDPOINT", "COM3"),
			Addr:      byte(envInt("GATE_IN_ADDR", 1)),
		},
		GateOut: GateConfig{
			Transport: env("GATE_OUT_TRANSPORT", "sim"),
			Endpoint:  env("GATE_OUT_ENDPOINT", "COM4"),
			Addr:      byte(envInt("GATE_OUT_ADDR", 2)),
		},
		EDC: TransportConfig{
			Transport: env("EDC_TRANSPORT", "sim"),
			Endpoint:  env("EDC_ENDPOINT", "127.0.0.1:5001"),
		},
		SyncEndpoint: env("SYNC_CLOUD_ENDPOINT", ""),
		SyncTick:     time.Duration(envInt("SYNC_TICK_SECONDS", 10)) * time.Second,
		SyncBatch:    envInt("SYNC_BATCH_SIZE", 200),
	}

	if c.NodeID == "" {
		return nil, fmt.Errorf("config: NODE_ID wajib diisi (rantai audit per-node, PRD §9.1)")
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("config: EDGE_DATABASE_URL wajib diisi")
	}
	if c.Env == "production" && len(c.JWTAccessSecret) < 32 {
		return nil, fmt.Errorf("config: JWT_ACCESS_SECRET < 32 char di production (PRD §14)")
	}
	return c, nil
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

func envDur(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
