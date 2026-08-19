package memstore

import (
	"context"
	"testing"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
)

func recordEvent(t *testing.T, s *Store, eventType string) {
	t.Helper()
	err := s.Record(context.Background(), audit.Event{
		EventType: eventType, Severity: audit.SevNormal, ActorRole: "System", Summary: "uji",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// TestAuditOutboxFillsAlongsideAuditLog — Record() harus mengisi audit_logs DAN antrean sync
// audit TERPISAH (task 8.5) dalam satu langkah, sama kontraknya dengan pgstore.
func TestAuditOutboxFillsAlongsideAuditLog(t *testing.T) {
	s := New("n1", nil)
	recordEvent(t, s, "GATE_OPEN")
	recordEvent(t, s, "GATE_CLOSE")

	if n := s.AuditOutbox().PendingAuditCount(); n != 2 {
		t.Fatalf("harus 2 item pending, got %d", n)
	}

	items := s.AuditOutbox().FetchPendingAudit(10)
	if len(items) != 2 {
		t.Fatalf("FetchPendingAudit harus 2, got %d", len(items))
	}
	// Urutan harus mengikuti urutan masuk (= urutan seq rantai), bukan acak.
	if items[0].Seq != 1 || items[1].Seq != 2 {
		t.Fatalf("urutan seq salah: %d, %d", items[0].Seq, items[1].Seq)
	}
	if items[0].Payload["event_type"] != "GATE_OPEN" || items[1].Payload["event_type"] != "GATE_CLOSE" {
		t.Fatalf("payload tak sesuai: %+v", items)
	}
}

// TestAuditOutboxMarkSentRemovesFromPending — item yang sudah dikirim tak boleh terus
// muncul di FetchPendingAudit (dikirim ulang selamanya akan membanjiri Cloud dengan duplikat).
func TestAuditOutboxMarkSentRemovesFromPending(t *testing.T) {
	s := New("n1", nil)
	recordEvent(t, s, "GATE_OPEN")
	recordEvent(t, s, "GATE_CLOSE")

	items := s.AuditOutbox().FetchPendingAudit(10)
	s.AuditOutbox().MarkAuditSent([]int64{items[0].ID})

	remaining := s.AuditOutbox().FetchPendingAudit(10)
	if len(remaining) != 1 || remaining[0].Seq != 2 {
		t.Fatalf("harus tersisa 1 item (seq 2), got %+v", remaining)
	}
	if n := s.AuditOutbox().PendingAuditCount(); n != 1 {
		t.Fatalf("PendingAuditCount harus 1, got %d", n)
	}
}

// TestAuditOutboxMarkFailedNeverPermanentlyFails — beda sengaja dari outbox biasa (yang punya
// batas 5 percobaan lalu FAILED permanen, lihat internal/outbox): audit TAK PERNAH menyerah,
// karena satu entri yang hilang berarti celah permanen di rantai bagi Cloud (§9.1).
func TestAuditOutboxMarkFailedNeverPermanentlyFails(t *testing.T) {
	s := New("n1", nil)
	recordEvent(t, s, "GATE_OPEN")

	items := s.AuditOutbox().FetchPendingAudit(10)
	id := items[0].ID
	for i := 0; i < 20; i++ {
		s.AuditOutbox().MarkAuditFailed(id, "cloud tak terjangkau")
	}

	// Setelah 20 kegagalan, item HARUS tetap pending — bukan FAILED permanen.
	remaining := s.AuditOutbox().FetchPendingAudit(10)
	if len(remaining) != 1 {
		t.Fatalf("item harus tetap pending setelah banyak kegagalan, got %d item pending", len(remaining))
	}
	if remaining[0].Attempts != 20 {
		t.Fatalf("attempts harus terhitung 20, got %d", remaining[0].Attempts)
	}
	if remaining[0].LastError == "" {
		t.Fatalf("LastError harus terisi")
	}
}
