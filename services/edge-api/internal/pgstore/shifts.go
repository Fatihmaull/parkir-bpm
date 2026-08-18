package pgstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
)

// ── Rekonsiliasi shift (§6.4/§12.6, task 7.4) ──

// OpenShift membuka shift baru. "Satu shift aktif per site" ditegakkan lewat unique index
// parsial DB (`idx_shifts_one_open`, migrasi 00007) — bukan cek SELECT-lalu-INSERT di sini,
// yang punya celah balapan antara dua panggilan konkuren. Constraint violation dari DB
// diterjemahkan jadi pesan yang jelas, bukan diteruskan mentah.
func (s *Store) OpenShift(ctx context.Context, operatorID string, openingFloat int64) (string, error) {
	id := ids.NewV7()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO shifts (id, tenant_id, site_id, operator_id, opened_at, opening_float, status)
		VALUES ($1, $2, $3, $4, now(), $5, 'OPEN')`,
		id, s.tenantID, s.siteID, operatorID, openingFloat)
	if err != nil {
		if isUniqueViolation(err) {
			return "", fmt.Errorf("pgstore: sudah ada shift terbuka untuk site ini — tutup dulu sebelum buka baru")
		}
		return "", fmt.Errorf("pgstore: OpenShift: %w", err)
	}
	return id, nil
}

// CloseShift mengunci baris shift (`FOR UPDATE`) sebelum menghitung total sistem, supaya
// dua permintaan tutup-shift konkuren atas shift yang sama tak dua-duanya lolos "status masih
// OPEN". Rumus selisih §6.4 persis: dilaporkan - (kas awal + tunai sistem). Selisih ≠ 0 WAJIB
// disertai note.
func (s *Store) CloseShift(ctx context.Context, shiftID string, declaredCash int64, note string) (memstore.ShiftView, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return memstore.ShiftView{}, fmt.Errorf("pgstore: CloseShift: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var operatorID, status string
	var openedAt time.Time
	var openingFloat int64
	err = tx.QueryRow(ctx, `
		SELECT operator_id, opened_at, opening_float, status FROM shifts
		 WHERE id = $1 AND site_id = $2 FOR UPDATE`,
		shiftID, s.siteID,
	).Scan(&operatorID, &openedAt, &openingFloat, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return memstore.ShiftView{}, fmt.Errorf("pgstore: shift %s tidak ditemukan", shiftID)
		}
		return memstore.ShiftView{}, fmt.Errorf("pgstore: CloseShift: %w", err)
	}
	if status != "OPEN" {
		return memstore.ShiftView{}, fmt.Errorf("pgstore: shift %s sudah ditutup", shiftID)
	}

	var systemCash, systemEDC, systemQRIS int64
	err = tx.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(amount) FILTER (WHERE method = 'CASH'), 0),
			COALESCE(SUM(amount) FILTER (WHERE method IN ('EDC_EMONEY', 'EDC_DEBIT')), 0),
			COALESCE(SUM(amount) FILTER (WHERE method IN ('QRIS', 'EWALLET')), 0)
		  FROM payments WHERE shift_id = $1 AND status = 'SETTLED'`,
		shiftID,
	).Scan(&systemCash, &systemEDC, &systemQRIS)
	if err != nil {
		return memstore.ShiftView{}, fmt.Errorf("pgstore: CloseShift jumlah: %w", err)
	}

	variance := declaredCash - (openingFloat + systemCash)
	if variance != 0 && note == "" {
		return memstore.ShiftView{}, fmt.Errorf("pgstore: selisih %d — wajib isi catatan alasan (§6.4)", variance)
	}
	newStatus := "CLOSED"
	if variance != 0 {
		newStatus = "VARIANCE"
	}

	var closedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE shifts
		   SET closed_at = now(), declared_cash = $1, system_cash = $2, system_edc = $3,
		       system_qris = $4, variance = $5, note = $6, status = $7
		 WHERE id = $8
		 RETURNING closed_at`,
		declaredCash, systemCash, systemEDC, systemQRIS, variance, nullIfEmpty(note), newStatus, shiftID,
	).Scan(&closedAt)
	if err != nil {
		return memstore.ShiftView{}, fmt.Errorf("pgstore: CloseShift update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return memstore.ShiftView{}, fmt.Errorf("pgstore: CloseShift: %w", err)
	}

	// Audit DI LUAR transaksi: baris shift sudah pasti tersimpan pada titik ini (tx sudah
	// commit) — kegagalan menulis audit (mis. pool sesak sesaat) tak boleh membuat penutupan
	// shift yang sudah sah tampak gagal bagi kasir. Ini bukan berarti audit boleh hilang
	// tanpa jejak: kegagalannya sendiri masuk log proses (slog), bukan disembunyikan diam-diam.
	if variance != 0 {
		sev := audit.SevWarning
		if abs64(variance) > s.cashVarianceThreshold(ctx) {
			sev = audit.SevCritical
		}
		if err := s.Record(ctx, audit.Event{
			EventType: "SHIFT_VARIANCE", Severity: sev,
			ActorLabel: operatorID, ActorRole: "Kasir",
			Summary: fmt.Sprintf("shift %s ditutup dengan selisih %d", shiftID, variance),
			Payload: map[string]any{"shift_id": shiftID, "variance": variance, "note": note},
		}); err != nil {
			slog.Error("pgstore: gagal menulis audit SHIFT_VARIANCE (shift tetap tertutup)",
				"shift_id", shiftID, "err", err)
		}
	}

	dc, v := declaredCash, variance
	return memstore.ShiftView{
		ID: shiftID, OperatorID: operatorID, OpenedAt: openedAt.Format(time.RFC3339),
		ClosedAt: closedAt.Format(time.RFC3339), OpeningFloat: openingFloat,
		DeclaredCash: &dc, SystemCash: systemCash, SystemEDC: systemEDC, SystemQRIS: systemQRIS,
		Variance: &v, Note: note, Status: newStatus,
	}, nil
}

// ShiftViews mengembalikan seluruh shift site ini, terbaru dulu.
func (s *Store) ShiftViews() []memstore.ShiftView {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, operator_id, opened_at, closed_at, opening_float, declared_cash,
		       COALESCE(system_cash, 0), COALESCE(system_edc, 0), COALESCE(system_qris, 0),
		       variance, COALESCE(note, ''), status
		  FROM shifts WHERE site_id = $1 ORDER BY opened_at DESC`,
		s.siteID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memstore.ShiftView
	for rows.Next() {
		var v memstore.ShiftView
		var openedAt time.Time
		var closedAt *time.Time
		var declaredCash, variance *int64
		if err := rows.Scan(&v.ID, &v.OperatorID, &openedAt, &closedAt, &v.OpeningFloat, &declaredCash,
			&v.SystemCash, &v.SystemEDC, &v.SystemQRIS, &variance, &v.Note, &v.Status); err != nil {
			return out
		}
		v.OpenedAt = openedAt.Format(time.RFC3339)
		if closedAt != nil {
			v.ClosedAt = closedAt.Format(time.RFC3339)
		}
		v.DeclaredCash = declaredCash
		v.Variance = variance
		out = append(out, v)
	}
	return out
}

func (s *Store) cashVarianceThreshold(ctx context.Context) int64 {
	var t int64
	if err := s.pool.QueryRow(ctx, `SELECT cash_variance_threshold FROM sites WHERE id = $1`, s.siteID).
		Scan(&t); err != nil {
		return 0 // site harus selalu ada (di-resolve saat New) — 0 = ambang paling ketat, aman.
	}
	return t
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
