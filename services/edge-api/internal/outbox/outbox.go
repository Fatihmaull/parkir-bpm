// Package outbox — pola transactional outbox (PRD §10.1). Antrean replikasi Edge→Cloud yang,
// pada implementasi pgx, ditulis dalam transaksi yang sama dengan data bisnisnya sehingga tidak
// ada transaksi tercatat yang gagal masuk antrean, dan tidak ada item antrean tanpa transaksi.
package outbox

import (
	"sync"
	"time"
)

type Status string

const (
	Pending Status = "PENDING"
	Sent    Status = "SENT"
	Failed  Status = "FAILED"
)

// Item — satu entri antrean sinkronisasi.
type Item struct {
	ID          int64          `json:"id"`
	Aggregate   string         `json:"aggregate"`    // vehicles_log|payments|audit_logs|...
	AggregateID string         `json:"aggregate_id"` // UUID (idempotensi di Cloud)
	Payload     map[string]any `json:"payload"`
	Status      Status         `json:"status"`
	Attempts    int            `json:"attempts"`
	LastError   string         `json:"last_error,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Store — kontrak outbox (di-fake/di-pgx-kan). Urutan FetchPending: created_at menaik (§10.2).
type Store interface {
	Enqueue(aggregate, aggregateID string, payload map[string]any)
	FetchPending(limit int) []Item
	MarkSent(ids []int64)
	MarkFailed(id int64, errMsg string)
	PendingCount() int
}

// Mem — implementasi in-memory (dipakai mode memstore & test).
type Mem struct {
	mu    sync.Mutex
	seq   int64
	items map[int64]*Item
}

func NewMem() *Mem { return &Mem{items: make(map[int64]*Item)} }

func (m *Mem) Enqueue(aggregate, aggregateID string, payload map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.items[m.seq] = &Item{
		ID: m.seq, Aggregate: aggregate, AggregateID: aggregateID,
		Payload: payload, Status: Pending, CreatedAt: time.Now(),
	}
}

func (m *Mem) FetchPending(limit int) []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	// items map bersifat tak-terurut; urutkan berdasar ID (proxy created_at monoton).
	ids := make([]int64, 0, len(m.items))
	for id, it := range m.items {
		if it.Status == Pending {
			ids = append(ids, id)
		}
	}
	sortInt64(ids)
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		out = append(out, *m.items[id])
	}
	return out
}

func (m *Mem) MarkSent(ids []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, id := range ids {
		if it, ok := m.items[id]; ok {
			it.Status = Sent
			it.LastError = ""
			_ = now
		}
	}
}

func (m *Mem) MarkFailed(id int64, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[id]; ok {
		it.Attempts++
		it.LastError = errMsg
		// Item gagal 5× → FAILED (tidak pernah dibuang, §10.2).
		if it.Attempts >= 5 {
			it.Status = Failed
		}
	}
}

// RequeueFailed mengembalikan item FAILED menjadi PENDING (cron outbox_retry §8.3). Kembalikan jumlah.
func (m *Mem) RequeueFailed() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, it := range m.items {
		if it.Status == Failed {
			it.Status = Pending
			it.Attempts = 0
			it.LastError = ""
			n++
		}
	}
	return n
}

func (m *Mem) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, it := range m.items {
		if it.Status == Pending {
			n++
		}
	}
	return n
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
