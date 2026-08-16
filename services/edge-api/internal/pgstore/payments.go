package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
)

// ── gate.Payments ──

func (s *Store) Begin(ctx context.Context, txID, method string, amount int64) (string, error) {
	id := ids.NewV7()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO payments (id, tenant_id, site_id, vehicles_log_id, method, amount, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING')`,
		id, s.tenantID, s.siteID, txID, method, amount)
	if err != nil {
		return "", fmt.Errorf("pgstore: Begin payment: %w", err)
	}
	return id, nil
}

// Settle menyimpan SELURUH rincian SettleInfo (tendered, change, approval code, masked PAN,
// dll) — memstore membuangnya (hanya mengubah status), karena in-memory memang tak punya
// tempat penyimpanan permanen untuk itu. Kolomnya sudah ada di skema persis untuk ini
// (§6.2.1 masked PAN); membuang data yang jelas-jelas diminta ditulis akan jadi regresi
// diam-diam dari maksud skema, bukan "parity" yang sah dengan memstore.
func (s *Store) Settle(ctx context.Context, payID string, info gate.SettleInfo) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE payments
		   SET status = 'SETTLED',
		       tendered = $1, change_given = $2, balance_after = $3,
		       approval_code = $4, batch_no = $5, masked_pan = $6, card_type = $7,
		       settled_at = now()
		 WHERE id = $8`,
		nullIfZero(info.Tendered), nullIfZero(info.ChangeGiven), nullIfZero(info.BalanceAfter),
		nullIfEmpty(info.ApprovalCode), nullIfEmpty(info.BatchNo), nullIfEmpty(info.MaskedPAN),
		nullIfEmpty(info.CardType), payID)
	if err != nil {
		return fmt.Errorf("pgstore: Settle: %w", err)
	}
	return nil
}

func (s *Store) Fail(ctx context.Context, payID, reason string) error {
	var raw json.RawMessage
	if reason != "" {
		raw, _ = json.Marshal(map[string]string{"fail_reason": reason})
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE payments SET status = 'FAILED', raw_response = COALESCE($1, raw_response) WHERE id = $2`,
		nullIfEmptyJSON(raw), payID)
	if err != nil {
		return fmt.Errorf("pgstore: Fail: %w", err)
	}
	return nil
}

func nullIfEmptyJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
