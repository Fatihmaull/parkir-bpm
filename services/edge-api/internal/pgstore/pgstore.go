// Package pgstore — implementasi PostgreSQL dari gatesvc.Store (task 5.1), menggantikan
// memstore saat EDGE_STORE=postgres. Kontraknya sengaja identik dengan memstore.Store
// (lihat gatesvc.Store) — yang berbeda hanya di mana data hidup.
//
// tenant_id & site_id di-resolve SEKALI saat New (dari TENANT_CODE/SITE_CODE di .env) dan
// dipakai di SETIAP query yang ditulis di sini — bukan diterima per-panggilan lewat parameter.
// Ini bukan kelonggaran, ini PENEGAKAN P6/§12.14 di lapisan repository: karena Edge adalah
// satu proses per lahan (satu tenant, satu site — realitas fisik "PC Admin per lahan"),
// mengikat identitas itu di konstruksi membuatnya MUSTAHIL lupa menyertakan tenant_id di query
// mana pun, dibanding mempercayai tiap pemanggil mengirimkannya benar tiap kali.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// Kompilasi memastikan Store memenuhi gatesvc.Store secara penuh — kalau ada metode yang
// terlewat/salah tanda tangan, ini gagal build, bukan gagal runtime saat EDGE_STORE=postgres.
var _ gatesvc.Store = (*Store)(nil)

// Store — repository pgx. Aman dipakai konkuren: pgxpool.Pool sendiri aman-konkuren, dan
// rantai audit (in-process, lihat audit.Chain) dijaga mu terpisah karena Next/Commit-nya
// harus berurutan ketat (P5) meski beberapa gerbang menulis audit bersamaan.
type Store struct {
	pool *pgxpool.Pool
	ob   *outbox.PG

	nodeID   string
	tenantID string // UUID tenants.id, di-resolve dari TENANT_CODE
	siteID   string // UUID sites.id, di-resolve dari SITE_CODE

	mu    sync.Mutex
	chain *audit.Chain
}

// New meresolusi tenant/site dari kodenya, memuat kembali rantai audit dari baris terakhir
// milik node ini (bukan mulai dari genesis — startup bukan berarti rantai baru, lihat
// CATATAN_KEPUTUSAN.md), dan memastikan baris ticket_counters untuk site ini ada.
//
// Gagal keras (bukan fallback ke memory) bila tenant/site tak ditemukan — konfigurasi
// EDGE_STORE=postgres yang menunjuk ke tenant/site yang salah lebih baik menghentikan startup
// daripada diam-diam melayani gerbang yang datanya tak pernah tersimpan ke mana pun.
func New(ctx context.Context, pool *pgxpool.Pool, tenantCode, siteCode, nodeID string) (*Store, error) {
	var tenantID string
	if err := pool.QueryRow(ctx, `SELECT id FROM tenants WHERE code = $1`, tenantCode).
		Scan(&tenantID); err != nil {
		return nil, fmt.Errorf("pgstore: tenant %q tidak ditemukan: %w", tenantCode, err)
	}

	var siteID string
	if err := pool.QueryRow(ctx, `SELECT id FROM sites WHERE tenant_id = $1 AND code = $2`,
		tenantID, siteCode).Scan(&siteID); err != nil {
		return nil, fmt.Errorf("pgstore: site %q (tenant %q) tidak ditemukan: %w", siteCode, tenantCode, err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO ticket_counters (site_id, counter) VALUES ($1, 0) ON CONFLICT (site_id) DO NOTHING`,
		siteID); err != nil {
		return nil, fmt.Errorf("pgstore: gagal menyiapkan ticket_counters: %w", err)
	}

	var lastSeq int64
	var lastHash string
	err := pool.QueryRow(ctx,
		`SELECT seq, current_hash FROM audit_logs WHERE node_id = $1 ORDER BY seq DESC LIMIT 1`,
		nodeID).Scan(&lastSeq, &lastHash)
	switch {
	case err == nil:
		// lanjutkan rantai yang sudah ada
	case errors.Is(err, pgx.ErrNoRows):
		lastSeq, lastHash = 0, "" // node baru → genesis (audit.NewChain menangani string kosong)
	default:
		return nil, fmt.Errorf("pgstore: gagal memuat state rantai audit: %w", err)
	}

	return &Store{
		pool: pool, ob: outbox.NewPG(pool),
		nodeID: nodeID, tenantID: tenantID, siteID: siteID,
		chain: audit.NewChain(nodeID, lastSeq, lastHash),
	}, nil
}

// Outbox mengembalikan antrean sinkronisasi (untuk sync agent, cron retry, & /health) — sama
// seperti memstore, dikembalikan sebagai interface outbox.Store.
func (s *Store) Outbox() outbox.Store { return s.ob }

// ocrWindowLimit — /api/v1/ocr-logs tak punya rantai hash yang bisa rusak kalau dipotong
// (beda dari audit_logs), jadi aman dibatasi ke N terbaru murni demi ukuran respons/kueri.
// AuditEntries/VerifyChain TIDAK dibatasi serupa — lihat komentar di audit.go: memotong
// jendela verifikasi rantai hash bisa melahirkan negatif-palsu "rantai rusak" (atau lebih
// buruk, celah verifikasi diam-diam) kalau tak dikerjakan sangat hati-hati. Untuk sekarang
// sengaja tetap scan penuh (P5: audit adalah jalur kritis, jangan dikorbankan demi kecepatan
// tanpa dipikir masak) — dicatat sebagai isu skala terbuka, bukan TODO tersembunyi, di
// CATATAN_KEPUTUSAN.md.
const ocrWindowLimit = 2000

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
