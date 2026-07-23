package wsbus

import "testing"

func TestPublishReachesSubscribers(t *testing.T) {
	h := NewHub()
	ch1, un1 := h.Subscribe()
	ch2, un2 := h.Subscribe()
	defer un1()
	defer un2()
	if h.Count() != 2 {
		t.Fatalf("count = %d, want 2", h.Count())
	}
	h.Publish("gate.state_changed", map[string]any{"to": "OPEN"})
	for i, ch := range []<-chan Event{ch1, ch2} {
		e := <-ch
		if e.Name != "gate.state_changed" || e.Data["to"] != "OPEN" {
			t.Fatalf("subscriber %d menerima event salah: %+v", i, e)
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, un := h.Subscribe()
	un()
	if h.Count() != 0 {
		t.Fatal("count harus 0 setelah unsubscribe")
	}
	if _, open := <-ch; open {
		t.Fatal("channel harus ditutup setelah unsubscribe")
	}
}

func TestPublishNonBlockingOnFullSubscriber(t *testing.T) {
	h := NewHub()
	_, un := h.Subscribe() // tidak pernah dibaca → akan penuh
	defer un()
	// Publish jauh melebihi kapasitas buffer; tidak boleh nge-block/panic.
	for i := 0; i < 1000; i++ {
		h.Publish("x", nil)
	}
}
