package auditsync

import (
	"context"
	"errors"
	"testing"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

type fakeSink struct {
	fail     bool
	received [][]outbox.AuditItem
}

func (s *fakeSink) SendAuditBatch(ctx context.Context, items []outbox.AuditItem) error {
	if s.fail {
		return errors.New("cloud unreachable")
	}
	s.received = append(s.received, items)
	return nil
}

// seed memakai memstore.Store nyata (satu-satunya AuditStore in-memory yang ada) — Agent tak
// peduli implementasi konkretnya, hanya kontrak outbox.AuditStore.
func seed(t *testing.T, s *memstore.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Record(context.Background(), audit.Event{
			EventType: "TEST_EVENT", Severity: audit.SevNormal, ActorRole: "System", Summary: "uji",
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
}

func TestDrainSendsAndMarksSent(t *testing.T) {
	s := memstore.New("n1", nil)
	seed(t, s, 3)
	sink := &fakeSink{}
	a := New(s.AuditOutbox(), sink, 200)

	sent, err := a.DrainOnce(context.Background())
	if err != nil || sent != 3 {
		t.Fatalf("drain: sent=%d err=%v", sent, err)
	}
	if s.AuditOutbox().PendingAuditCount() != 0 {
		t.Fatalf("harus tidak ada pending setelah sukses, got %d", s.AuditOutbox().PendingAuditCount())
	}
	sent, _ = a.DrainOnce(context.Background())
	if sent != 0 {
		t.Fatalf("drain kedua harus 0, got %d", sent)
	}
}

func TestFailedBatchStaysPendingForeverAndRetries(t *testing.T) {
	s := memstore.New("n1", nil)
	seed(t, s, 2)
	sink := &fakeSink{fail: true}
	a := New(s.AuditOutbox(), sink, 200)

	// Beda sengaja dari syncagent: berapa pun kali gagal, item TAK PERNAH pindah ke FAILED
	// permanen (satu entri audit hilang = celah rantai selamanya bagi Cloud, §9.1).
	for i := 0; i < 10; i++ {
		if _, err := a.DrainOnce(context.Background()); err == nil {
			t.Fatal("harus error saat Cloud down")
		}
	}
	if n := s.AuditOutbox().PendingAuditCount(); n != 2 {
		t.Fatalf("item harus tetap pending walau gagal berkali-kali, got %d", n)
	}

	sink.fail = false
	sent, err := a.DrainOnce(context.Background())
	if err != nil || sent != 2 {
		t.Fatalf("setelah pulih: sent=%d err=%v", sent, err)
	}
}

func TestBackoffEscalates(t *testing.T) {
	s := memstore.New("n1", nil)
	seed(t, s, 1)
	a := New(s.AuditOutbox(), &fakeSink{fail: true}, 200)
	base := a.nextDelay(10)
	if base != 10 {
		t.Fatalf("tanpa kegagalan harus base, got %v", base)
	}
	_, _ = a.DrainOnce(context.Background())
	if a.nextDelay(10) != a.backoff[0] {
		t.Fatalf("setelah 1 gagal harus backoff[0]")
	}
}
