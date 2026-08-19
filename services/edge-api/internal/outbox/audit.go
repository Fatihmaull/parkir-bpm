package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditItem — satu baris audit_logs siap-kirim ke Cloud. Dikunci NodeID+Seq, bukan
// aggregate_id seperti Item biasa — kontinuitas rantai (§9.4) menuntut Cloud tahu URUTAN
// per node, bukan cuma identitas tiap baris. Lihat db/migrations/00008 untuk kenapa ini
// tabel/jalur terpisah dari sync_outbox (task 8.5).
type AuditItem struct {
	ID        int64          `json:"id"`
	NodeID    string         `json:"node_id"`
	Seq       int64          `json:"seq"`
	Payload   map[string]any `json:"payload"`
	Status    Status         `json:"status"`
	Attempts  int            `json:"attempts"`
	LastError string         `json:"last_error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// AuditStore — sisi BACA/TANDAI antrean sync audit (dipakai internal/auditsync). Enqueue
// sengaja TIDAK ada di sini — itu bagian dari transaksi tulis audit_logs sendiri
// (EnqueueAuditTx, dipanggil dari internal/pgstore.Record), bukan operasi berdiri sendiri.
type AuditStore interface {
	FetchPendingAudit(limit int) []AuditItem
	MarkAuditSent(ids []int64)
	MarkAuditFailed(id int64, errMsg string)
	PendingAuditCount() int
}

// EnqueueAuditTx menulis satu baris audit_sync_outbox lewat querier apa pun (pool atau
// pgx.Tx) — dipanggil DALAM transaksi yang sama dengan INSERT audit_logs (lihat
// pgstore.Record), supaya tak pernah ada baris audit_logs tanpa entri sync, atau sebaliknya.
func EnqueueAuditTx(ctx context.Context, q querier, nodeID string, seq int64, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx,
		`INSERT INTO audit_sync_outbox (node_id, seq, payload) VALUES ($1, $2, $3)`,
		nodeID, seq, b)
	return err
}

// AuditPG — implementasi AuditStore di atas audit_sync_outbox.
type AuditPG struct {
	pool *pgxpool.Pool
}

func NewAuditPG(pool *pgxpool.Pool) *AuditPG { return &AuditPG{pool: pool} }

func (p *AuditPG) FetchPendingAudit(limit int) []AuditItem {
	rows, err := p.pool.Query(context.Background(), `
		SELECT id, node_id, seq, payload, status, attempts, COALESCE(last_error, ''), created_at
		  FROM audit_sync_outbox WHERE status = 'PENDING' ORDER BY node_id, seq ASC LIMIT $1`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []AuditItem
	for rows.Next() {
		var it AuditItem
		var payload []byte
		var status string
		if err := rows.Scan(&it.ID, &it.NodeID, &it.Seq, &payload, &status,
			&it.Attempts, &it.LastError, &it.CreatedAt); err != nil {
			return out
		}
		it.Status = Status(status)
		_ = json.Unmarshal(payload, &it.Payload)
		out = append(out, it)
	}
	return out
}

func (p *AuditPG) MarkAuditSent(ids []int64) {
	if len(ids) == 0 {
		return
	}
	_, _ = p.pool.Exec(context.Background(),
		`UPDATE audit_sync_outbox SET status = 'SENT', sent_at = now(), last_error = NULL WHERE id = ANY($1)`, ids)
}

// MarkAuditFailed HANYA mencatat percobaan & pesan gagal — status TETAP PENDING selamanya,
// tak pernah pindah ke FAILED seperti outbox biasa. Beda sengaja dari outbox.PG.MarkFailed:
// satu entri audit yang menyerah berarti CELAH PERMANEN di rantai bagi Cloud — semua entri
// SESUDAHNYA ikut tak terverifikasi selamanya. Data bisnis yang gagal terus bisa direkonsiliasi
// manual belakangan; audit tak punya jalan "belakangan" sebaik itu begitu ada celah.
func (p *AuditPG) MarkAuditFailed(id int64, errMsg string) {
	_, _ = p.pool.Exec(context.Background(), `
		UPDATE audit_sync_outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
		id, errMsg)
}

func (p *AuditPG) PendingAuditCount() int {
	var n int
	_ = p.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_sync_outbox WHERE status = 'PENDING'`).Scan(&n)
	return n
}

var _ AuditStore = (*AuditPG)(nil)
