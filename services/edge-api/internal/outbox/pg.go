package outbox

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier — subset pgx dipakai di sini, dipenuhi baik *pgxpool.Pool maupun pgx.Tx. Lewat ini
// EnqueueTx bisa menulis ke sync_outbox di dalam transaksi bisnis yang sama (lihat komentar
// paket), sedangkan Enqueue (kontrak Store) tetap jalan sendiri lewat pool.
type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// PG — implementasi Store di atas tabel sync_outbox (task 5.1). Enqueue lewat pool memakai
// statement Postgres sendiri; EnqueueTx dipakai internal repository pgx supaya baris
// sync_outbox masuk dalam transaksi yang sama dengan data bisnisnya (P1/D4 — tak boleh ada
// transaksi tercatat tanpa entri outbox, atau sebaliknya).
type PG struct {
	pool *pgxpool.Pool
}

func NewPG(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

func (p *PG) Enqueue(aggregate, aggregateID string, payload map[string]any) {
	// Kontrak Store tak mengembalikan error (meniru Mem). Kegagalan di sini jarang — pool
	// down berarti seluruh edge-api sudah tak sehat, dan itu sudah kelihatan lewat /health.
	_ = EnqueueTx(context.Background(), p.pool, aggregate, aggregateID, payload)
}

// EnqueueTx menulis satu baris sync_outbox lewat querier apa pun — pool atau pgx.Tx (dalam
// transaksi bisnis yang sedang berjalan). Dipakai internal oleh internal/pgstore.
func EnqueueTx(ctx context.Context, q querier, aggregate, aggregateID string, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx,
		`INSERT INTO sync_outbox (aggregate, aggregate_id, payload) VALUES ($1, $2, $3)`,
		aggregate, aggregateID, b)
	return err
}

func (p *PG) FetchPending(limit int) []Item {
	rows, err := p.pool.Query(context.Background(),
		`SELECT id, aggregate, aggregate_id, payload, status, attempts, COALESCE(last_error, ''), created_at
		   FROM sync_outbox WHERE status = 'PENDING' ORDER BY created_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var it Item
		var payload []byte
		var status string
		if err := rows.Scan(&it.ID, &it.Aggregate, &it.AggregateID, &payload, &status,
			&it.Attempts, &it.LastError, &it.CreatedAt); err != nil {
			return out
		}
		it.Status = Status(status)
		_ = json.Unmarshal(payload, &it.Payload)
		out = append(out, it)
	}
	return out
}

func (p *PG) MarkSent(ids []int64) {
	if len(ids) == 0 {
		return
	}
	_, _ = p.pool.Exec(context.Background(),
		`UPDATE sync_outbox SET status = 'SENT', sent_at = now(), last_error = NULL WHERE id = ANY($1)`, ids)
}

func (p *PG) MarkFailed(id int64, errMsg string) {
	// attempts >= 5 → FAILED, meniru Mem (§10.2: tak pernah dibuang).
	_, _ = p.pool.Exec(context.Background(), `
		UPDATE sync_outbox
		   SET attempts = attempts + 1,
		       last_error = $2,
		       status = CASE WHEN attempts + 1 >= 5 THEN 'FAILED' ELSE status END
		 WHERE id = $1`, id, errMsg)
}

func (p *PG) RequeueFailed() int {
	tag, err := p.pool.Exec(context.Background(),
		`UPDATE sync_outbox SET status = 'PENDING', attempts = 0, last_error = NULL WHERE status = 'FAILED'`)
	if err != nil {
		return 0
	}
	return int(tag.RowsAffected())
}

func (p *PG) PendingCount() int {
	var n int
	_ = p.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sync_outbox WHERE status = 'PENDING'`).Scan(&n)
	return n
}

var _ Store = (*PG)(nil)
