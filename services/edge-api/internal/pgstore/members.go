package pgstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
)

// ── gate.Members (anti-passback §8.2) ──

// ValidateEntry mengunci baris membership (SELECT ... FOR UPDATE) sebelum menilai keputusan
// dan menulis presence — tanpa itu dua tap RFID kembar yang datang nyaris bersamaan bisa
// dua-duanya lolos ANTIPASSBACK_VIOLATION check sebelum salah satu sempat menulis presence=IN.
// memstore aman dari race ini "gratis" lewat satu mutex proses; di Postgres kuncinya eksplisit.
func (s *Store) ValidateEntry(ctx context.Context, uid string) (gate.MemberDecision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gate.MemberDecision{}, fmt.Errorf("pgstore: ValidateEntry: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var membershipID, status, presence string
	var validUntil time.Time
	var siteScope []string
	err = tx.QueryRow(ctx, `
		SELECT id, status, presence, valid_until, site_scope
		  FROM memberships
		 WHERE tenant_id = $1 AND rfid_uid = $2
		 FOR UPDATE`,
		s.tenantID, uid,
	).Scan(&membershipID, &status, &presence, &validUntil, &siteScope)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.MemberDecision{Allowed: false, Reason: "NOT_MEMBER"}, nil
		}
		return gate.MemberDecision{}, fmt.Errorf("pgstore: ValidateEntry: %w", err)
	}

	// site_scope kosong = berlaku di semua site tenant (§11.2); tak kosong = harus mencakup
	// site ini. Di luar cakupan diperlakukan sama seperti bukan member DI SINI.
	if len(siteScope) > 0 && !slices.Contains(siteScope, s.siteID) {
		return gate.MemberDecision{Allowed: false, Reason: "NOT_MEMBER"}, nil
	}
	if status == "blocked" {
		return gate.MemberDecision{Allowed: false, Reason: "BLOCKED"}, nil
	}
	// valid_until adalah DATE (berlaku s.d. akhir hari itu) — expired setelah lewat hari itu.
	if status == "expired" || !time.Now().Before(validUntil.AddDate(0, 0, 1)) {
		return gate.MemberDecision{Allowed: false, Reason: "EXPIRED"}, nil
	}
	if presence == "IN" {
		return gate.MemberDecision{Allowed: false, Reason: "ANTIPASSBACK_VIOLATION"}, nil
	}

	if _, err := tx.Exec(ctx,
		`UPDATE memberships SET presence = 'IN', presence_since = now() WHERE id = $1`,
		membershipID); err != nil {
		return gate.MemberDecision{}, fmt.Errorf("pgstore: ValidateEntry set presence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return gate.MemberDecision{}, fmt.Errorf("pgstore: ValidateEntry: %w", err)
	}
	return gate.MemberDecision{Allowed: true, MembershipID: membershipID}, nil
}

// AddMember — pendaftaran/impor member dari dashboard (§12.13). Kontrak gate ini (meniru
// memstore persis) tak menyediakan context maupun jalur error: itu keterbatasan interface
// yang sudah ada, bukan sesuatu yang diperbaiki diam-diam di sini (mengubah tanda tangan
// melanggar "interface identik" task 5.1). Kegagalan hanya dicatat log; pemanggil
// (cmd/edge-api/dashboard.go) tetap membaca id kosong sebagai sinyal gagal bila perlu —
// tercatat sebagai keterbatasan yang diwariskan, lihat CATATAN_KEPUTUSAN.md.
func (s *Store) AddMember(uid string, plates []string, vehicleType string, validUntil time.Time) string {
	id := ids.NewV7()
	_, err := s.pool.Exec(context.Background(), `
		INSERT INTO memberships (id, tenant_id, rfid_uid, holder_name, plates, vehicle_type, valid_from, valid_until)
		VALUES ($1, $2, $3, $3, $4, $5, CURRENT_DATE, $6)
		ON CONFLICT (tenant_id, rfid_uid) DO UPDATE
		   SET plates = EXCLUDED.plates, vehicle_type = EXCLUDED.vehicle_type, valid_until = EXCLUDED.valid_until`,
		id, s.tenantID, uid, plates, vehicleType, validUntil)
	if err != nil {
		slog.Error("pgstore: AddMember gagal", "uid", uid, "err", err)
		return ""
	}
	return id
}

// ── CRON job logic (§8.3) — idempoten ──

func (s *Store) ExpireMemberships(now time.Time) int {
	ct, err := s.pool.Exec(context.Background(), `
		UPDATE memberships SET status = 'expired'
		 WHERE tenant_id = $1 AND status = 'active' AND valid_until < $2::date`,
		s.tenantID, now)
	if err != nil {
		slog.Error("pgstore: ExpireMemberships gagal", "err", err)
		return 0
	}
	return int(ct.RowsAffected())
}

func (s *Store) ResetStalePresence(now time.Time, hours int) int {
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	ct, err := s.pool.Exec(context.Background(), `
		UPDATE memberships SET presence = 'OUT'
		 WHERE tenant_id = $1 AND presence = 'IN' AND presence_since < $2`,
		s.tenantID, cutoff)
	if err != nil {
		slog.Error("pgstore: ResetStalePresence gagal", "err", err)
		return 0
	}
	return int(ct.RowsAffected())
}
