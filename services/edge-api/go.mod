module github.com/jabar-creative/parkir/edge-api

go 1.22

// Jalankan `go mod tidy` untuk mengisi go.sum & versi indirect (Go belum terpasang saat scaffold).
require (
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.6.0
	github.com/robfig/cron/v3 v3.0.1
)
