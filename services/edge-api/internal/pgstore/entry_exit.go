package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// ── gate.Store (entry) ──

func (s *Store) CreateDraft(ctx context.Context) (string, error) {
	id := ids.NewV7()
	// vehicle_type "mobil" tetap: memstore juga tak pernah mengubahnya (CommitInPremises tak
	// menerima parameter vehicle_type) — dipertahankan sama persis, bukan celah baru di sini.
	_, err := s.pool.Exec(ctx,
		`INSERT INTO vehicles_log (id, tenant_id, site_id, status, vehicle_type, entry_time)
		 VALUES ($1, $2, $3, 'DRAFT', 'mobil', now())`,
		id, s.tenantID, s.siteID)
	if err != nil {
		return "", fmt.Errorf("pgstore: CreateDraft: %w", err)
	}
	return id, nil
}

func (s *Store) CommitInPremises(ctx context.Context, txID, ticketCode, plate, membershipID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: CommitInPremises: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit sukses

	ct, err := tx.Exec(ctx, `
		UPDATE vehicles_log
		   SET status = 'IN_PREMISES', ticket_code = $1, plate_in = $2, membership_id = $3,
		       entry_time = COALESCE(entry_time, now()), updated_at = now()
		 WHERE id = $4 AND tenant_id = $5 AND site_id = $6`,
		ticketCode, plate, nullIfEmpty(membershipID), txID, s.tenantID, s.siteID)
	if err != nil {
		return fmt.Errorf("pgstore: CommitInPremises: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("pgstore: tx %s tidak ditemukan", txID)
	}

	// Outbox transaksional (§10.1) — baris sync_outbox masuk dalam transaksi yang sama, jadi
	// tak pernah ada transaksi tercatat tanpa entri outbox, atau sebaliknya.
	if err := outbox.EnqueueTx(ctx, tx, "vehicles_log", txID, map[string]any{
		"id": txID, "status": "IN_PREMISES", "ticket_code": ticketCode, "membership_id": membershipID,
	}); err != nil {
		return fmt.Errorf("pgstore: CommitInPremises outbox: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Store) Void(ctx context.Context, txID, reason string) error {
	// memstore.Void tak melaporkan error bila tx tak ditemukan (no-op diam) — dipertahankan.
	// reason disimpan ke flags (memstore tak punya tempat menyimpannya sama sekali — bukan
	// pengurangan perilaku, DB di sini punya kapasitas yang memstore tak punya).
	_, err := s.pool.Exec(ctx, `
		UPDATE vehicles_log
		   SET status = 'VOID',
		       flags = CASE WHEN $1 <> '' THEN array_append(flags, 'void:' || $1) ELSE flags END,
		       updated_at = now()
		 WHERE id = $2 AND tenant_id = $3 AND site_id = $4`,
		reason, txID, s.tenantID, s.siteID)
	if err != nil {
		return fmt.Errorf("pgstore: Void: %w", err)
	}
	return nil
}

// ── gate.ExitStore ──

func (s *Store) Lookup(ctx context.Context, key gate.LookupKey) (gate.ExitTx, error) {
	var tx gate.ExitTx
	err := s.pool.QueryRow(ctx, `
		SELECT v.id, v.vehicle_type, v.entry_time, COALESCE(v.img_in_key, ''),
		       COALESCE(m.status = 'active' AND m.valid_until >= CURRENT_DATE, false)
		  FROM vehicles_log v
		  LEFT JOIN memberships m ON m.id = v.membership_id
		 WHERE v.status = 'IN_PREMISES' AND v.tenant_id = $1 AND v.site_id = $2
		   AND ( ($3 <> '' AND v.ticket_code = $3)
		      OR ($4 <> '' AND v.plate_in = $4)
		      OR ($5 <> '' AND m.rfid_uid = $5) )
		 LIMIT 1`,
		s.tenantID, s.siteID, key.Ticket, key.Plate, key.UID,
	).Scan(&tx.ID, &tx.VehicleType, &tx.EntryTime, &tx.ImgInKey, &tx.MembershipActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.ExitTx{Found: false}, nil
		}
		return gate.ExitTx{}, fmt.Errorf("pgstore: Lookup: %w", err)
	}
	tx.Found = true
	return tx, nil
}

func (s *Store) Complete(ctx context.Context, txID string, amount int64, plateOut string, flags []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: Complete: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if flags == nil {
		// flags TEXT[] NOT NULL DEFAULT '{}' — nil slice Go di-encode pgx sebagai SQL NULL,
		// bukan array kosong, dan membentur constraint itu. Ditemukan lewat uji integrasi
		// terhadap Postgres sungguhan (memstore tak pernah menyentuh masalah ini — map Go
		// menerima nil slice tanpa keluhan).
		flags = []string{}
	}

	var membershipID *string
	err = tx.QueryRow(ctx, `
		UPDATE vehicles_log
		   SET status = 'COMPLETED', amount = $1, plate_out = $2, flags = $3,
		       exit_time = now(), updated_at = now()
		 WHERE id = $4 AND tenant_id = $5 AND site_id = $6
		 RETURNING membership_id`,
		amount, plateOut, flags, txID, s.tenantID, s.siteID,
	).Scan(&membershipID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("pgstore: tx %s tidak ditemukan", txID)
		}
		return fmt.Errorf("pgstore: Complete: %w", err)
	}

	// Anti-passback: kendaraan member keluar → presence OUT (§8.2), sama seperti memstore.
	if membershipID != nil {
		if _, err := tx.Exec(ctx, `UPDATE memberships SET presence = 'OUT' WHERE id = $1`,
			*membershipID); err != nil {
			return fmt.Errorf("pgstore: Complete reset presence: %w", err)
		}
	}

	if err := outbox.EnqueueTx(ctx, tx, "vehicles_log", txID, map[string]any{
		"id": txID, "status": "COMPLETED", "amount": amount, "flags": flags,
	}); err != nil {
		return fmt.Errorf("pgstore: Complete outbox: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkDispute(ctx context.Context, txID string) error {
	// memstore juga no-op diam bila tak ditemukan — dipertahankan.
	_, err := s.pool.Exec(ctx,
		`UPDATE vehicles_log SET status = 'DISPUTE', updated_at = now() WHERE id = $1 AND tenant_id = $2 AND site_id = $3`,
		txID, s.tenantID, s.siteID)
	if err != nil {
		return fmt.Errorf("pgstore: MarkDispute: %w", err)
	}
	return nil
}
