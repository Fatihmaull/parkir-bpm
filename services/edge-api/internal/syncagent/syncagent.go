// Package syncagent — agen sinkronisasi Edge→Cloud (PRD §10.2). Menguras outbox secara batch,
// backoff eksponensial saat gagal, urutan dijaga, item tak pernah dibuang. Berjalan sebagai
// goroutine di edge-api; Cloud adalah tujuan replikasi, bukan dependensi runtime (P1).
package syncagent

import (
	"context"
	"log/slog"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// Sink — tujuan pengiriman batch (cloud-api). Kembalikan error → item tetap PENDING & di-retry.
type Sink interface {
	SendBatch(ctx context.Context, items []outbox.Item) error
}

type Agent struct {
	ob        outbox.Store
	sink      Sink
	batchSize int
	log       *slog.Logger

	backoff  []time.Duration
	failures int
}

func New(ob outbox.Store, sink Sink, batchSize int) *Agent {
	if batchSize <= 0 {
		batchSize = 200
	}
	return &Agent{
		ob: ob, sink: sink, batchSize: batchSize, log: slog.Default(),
		// Backoff eksponensial (§10.2): 10s → 30s → 2m → 10m → 30m.
		backoff: []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute},
	}
}

// DrainOnce mengirim satu batch. Kembalikan jumlah item terkirim.
// Deterministik & dapat diuji tanpa timer.
func (a *Agent) DrainOnce(ctx context.Context) (int, error) {
	items := a.ob.FetchPending(a.batchSize)
	if len(items) == 0 {
		a.failures = 0
		return 0, nil
	}
	if err := a.sink.SendBatch(ctx, items); err != nil {
		// Gagal → tandai tiap item gagal (naikkan attempts), backoff dinaikkan.
		for _, it := range items {
			a.ob.MarkFailed(it.ID, err.Error())
		}
		a.failures++
		a.log.Warn("sync batch gagal", "err", err, "size", len(items), "failures", a.failures)
		return 0, err
	}
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	a.ob.MarkSent(ids)
	a.failures = 0
	return len(items), nil
}

// nextDelay mengembalikan jeda berikutnya sesuai jumlah kegagalan beruntun.
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

// Run menjalankan loop sinkronisasi hingga ctx dibatalkan. `tick` = interval normal (§10.2: 10s).
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
				next = a.nextDelay(tick) // perlambat saat Cloud down
			}
			timer.Reset(next)
		}
	}
}
