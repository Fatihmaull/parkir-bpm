// Package auditsync — agen sinkronisasi audit_logs Edge→Cloud (task 8.5), TERPISAH dari
// syncagent (vehicles_log/payments). Struktur sengaja mirip syncagent (backoff, urutan
// dijaga, item tak pernah dibuang) tapi diduplikasi sebagai tipe sendiri, bukan dipaksa
// masuk kontrak Sink/Store syncagent yang berbentuk aggregate_id — dan supaya kegagalan
// atau backoff salah satu jalur TIDAK PERNAH menahan yang lain. Lihat db/migrations/00008
// & internal/outbox/audit.go untuk alasan pemisahan sampai ke lapisan data.
package auditsync

import (
	"context"
	"log/slog"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// Sink — tujuan pengiriman batch audit (cloud-api). Kembalikan error → item tetap
// belum-terkirim & di-retry; TAK PERNAH menyerah (lihat outbox.AuditPG.MarkAuditFailed).
type Sink interface {
	SendAuditBatch(ctx context.Context, items []outbox.AuditItem) error
}

type Agent struct {
	ob        outbox.AuditStore
	sink      Sink
	batchSize int
	log       *slog.Logger

	backoff  []time.Duration
	failures int
}

func New(ob outbox.AuditStore, sink Sink, batchSize int) *Agent {
	if batchSize <= 0 {
		batchSize = 200
	}
	return &Agent{
		ob: ob, sink: sink, batchSize: batchSize, log: slog.Default(),
		backoff: []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute},
	}
}

// DrainOnce mengirim satu batch. Kembalikan jumlah item terkirim.
func (a *Agent) DrainOnce(ctx context.Context) (int, error) {
	items := a.ob.FetchPendingAudit(a.batchSize)
	if len(items) == 0 {
		a.failures = 0
		return 0, nil
	}
	if err := a.sink.SendAuditBatch(ctx, items); err != nil {
		for _, it := range items {
			a.ob.MarkAuditFailed(it.ID, err.Error())
		}
		a.failures++
		a.log.Warn("audit sync batch gagal", "err", err, "size", len(items), "failures", a.failures)
		return 0, err
	}
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	a.ob.MarkAuditSent(ids)
	a.failures = 0
	return len(items), nil
}

func (a *Agent) nextDelay(base time.Duration) time.Duration {
	if a.failures == 0 {
		return base
	}
	i := a.failures - 1
	if i >= len(a.backoff) {
		i = len(a.backoff) - 1
	}
	return a.backoff[i]
}

// Run menjalankan loop sinkronisasi hingga ctx dibatalkan.
func (a *Agent) Run(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	timer := time.NewTimer(tick)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, err := a.DrainOnce(ctx)
			next := tick
			if err != nil {
				next = a.nextDelay(tick)
			}
			timer.Reset(next)
		}
	}
}
