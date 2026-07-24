package syncagent

import (
	"context"
	"errors"
	"testing"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

type fakeSink struct {
	fail     bool
	received [][]outbox.Item
}

func (s *fakeSink) SendBatch(ctx context.Context, items []outbox.Item) error {
	if s.fail {
		return errors.New("cloud unreachable")
	}
	s.received = append(s.received, items)
	return nil
}

func seed(ob *outbox.Mem, n int) {
	for i := 0; i < n; i++ {
		ob.Enqueue("vehicles_log", "id-"+string(rune('a'+i)), map[string]any{"n": i})
	}
}

func TestDrainSendsAndMarksSent(t *testing.T) {
	ob := outbox.NewMem()
	seed(ob, 3)
	sink := &fakeSink{}
	a := New(ob, sink, 200)

	sent, err := a.DrainOnce(context.Background())
	if err != nil || sent != 3 {
		t.Fatalf("drain: sent=%d err=%v", sent, err)
	}
	if ob.PendingCount() != 0 {
		t.Fatalf("harus tidak ada pending setelah sukses, got %d", ob.PendingCount())
	}
	// Drain kedua tidak mengirim apa pun.
	sent, _ = a.DrainOnce(context.Background())
	if sent != 0 {
		t.Fatalf("drain kedua harus 0, got %d", sent)
	}
}

func TestFailedBatchStaysPendingAndRetries(t *testing.T) {
	ob := outbox.NewMem()
	seed(ob, 2)
	sink := &fakeSink{fail: true}
	a := New(ob, sink, 200)

	if _, err := a.DrainOnce(context.Background()); err == nil {
		t.Fatal("harus error saat Cloud down")
	}
	// Item tetap PENDING (offline-first: antrean menumpuk, tak hilang — §10.4).
	if ob.PendingCount() != 2 {
		t.Fatalf("item harus tetap pending, got %d", ob.PendingCount())
	}
	// Cloud pulih → drain berhasil.
	sink.fail = false
	sent, err := a.DrainOnce(context.Background())
	if err != nil || sent != 2 {
		t.Fatalf("setelah pulih: sent=%d err=%v", sent, err)
	}
}

func TestItemFailsFiveTimesBecomesFailed(t *testing.T) {
	ob := outbox.NewMem()
	seed(ob, 1)
	sink := &fakeSink{fail: true}
	a := New(ob, sink, 200)
	for i := 0; i < 5; i++ {
		_, _ = a.DrainOnce(context.Background())
	}
	// Setelah 5 kegagalan → FAILED, tidak lagi PENDING (tapi tetap tersimpan, tidak dibuang).
	if ob.PendingCount() != 0 {
		t.Fatalf("item harus jadi FAILED (tak pending) setelah 5x, pending=%d", ob.PendingCount())
	}
}

func TestBackoffEscalates(t *testing.T) {
	ob := outbox.NewMem()
	seed(ob, 1)
	a := New(ob, &fakeSink{fail: true}, 200)
	base := a.nextDelay(10) // failures=0 → base
	if base != 10 {
		t.Fatalf("tanpa kegagalan harus base, got %v", base)
	}
	_, _ = a.DrainOnce(context.Background()) // failures=1
	if a.nextDelay(10) != a.backoff[0] {
		t.Fatalf("setelah 1 gagal harus backoff[0]")
	}
}
