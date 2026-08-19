package memstore

import (
	"sort"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// auditOutboxItem — padanan in-memory outbox.AuditItem (task 8.5). Kunci mapnya (int64,
// dinaikkan tiap Record) BUKAN seq audit — seq milik rantai per node, sedangkan kunci di
// sini murni identitas baris antrean, sama peran seperti kolom `id` di audit_sync_outbox.
type auditOutboxItem struct {
	seq       int64
	payload   map[string]any
	sent      bool
	attempts  int
	lastError string
	createdAt time.Time
}

// AuditOutbox memenuhi gatesvc.Store — jalur sync audit_logs terpisah dari Outbox() biasa,
// sama alasannya dengan pgstore (lihat internal/outbox/audit.go).
func (s *Store) AuditOutbox() outbox.AuditStore { return (*memAuditOutbox)(s) }

// memAuditOutbox — pembungkus tipis di atas *Store, supaya metode outbox.AuditStore tak
// membanjiri namespace metode Store utama (pola sama seperti seharusnya kalau outbox.Mem
// dipisah dari Store — di sini dipertahankan menyatu karena semua tetap di bawah s.mu yang
// sama, in-memory tak punya isu konkurensi lintas-tabel seperti Postgres).
type memAuditOutbox Store

func (m *memAuditOutbox) s() *Store { return (*Store)(m) }

func (m *memAuditOutbox) FetchPendingAudit(limit int) []outbox.AuditItem {
	s := m.s()
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]int64, 0, len(s.auditOutbox))
	for id, it := range s.auditOutbox {
		if !it.sent {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] }) // urutan masuk = urutan seq

	var out []outbox.AuditItem
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		it := s.auditOutbox[id]
		out = append(out, outbox.AuditItem{
			ID: id, NodeID: s.nodeID, Seq: it.seq, Payload: it.payload,
			Status: outbox.Pending, Attempts: it.attempts, LastError: it.lastError,
			CreatedAt: it.createdAt,
		})
	}
	return out
}

func (m *memAuditOutbox) MarkAuditSent(ids []int64) {
	s := m.s()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if it, ok := s.auditOutbox[id]; ok {
			it.sent = true
			it.lastError = ""
		}
	}
}

func (m *memAuditOutbox) MarkAuditFailed(id int64, errMsg string) {
	s := m.s()
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.auditOutbox[id]; ok {
		it.attempts++
		it.lastError = errMsg
		// TETAP pending selamanya — lihat komentar outbox.AuditPG.MarkAuditFailed: satu
		// entri audit yang menyerah berarti celah permanen di rantai bagi Cloud.
	}
}

func (m *memAuditOutbox) PendingAuditCount() int {
	s := m.s()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, it := range s.auditOutbox {
		if !it.sent {
			n++
		}
	}
	return n
}
