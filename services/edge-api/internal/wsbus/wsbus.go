// Package wsbus — event bus real-time (PRD §5.6/§13.1). Fan-out event dari state machine,
// hardware, pembayaran, dan alert ke banyak subscriber (POS, Field Monitor) via WebSocket.
// Hub in-process (tanpa Redis — §4.3): satu Edge Node, satu proses.
package wsbus

import "sync"

// Event WS (PRD §13.1).
type Event struct {
	Name string         `json:"event"`
	Data map[string]any `json:"data,omitempty"`
}

// Hub menyiarkan Event ke seluruh subscriber aktif.
type Hub struct {
	mu     sync.RWMutex
	subs   map[int]chan Event
	nextID int
	buf    int
}

func NewHub() *Hub {
	return &Hub{subs: make(map[int]chan Event), buf: 32}
}

// Subscribe mendaftarkan subscriber baru. Kembalikan channel event & fungsi unsubscribe.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	ch := make(chan Event, h.buf)
	h.subs[id] = ch
	return ch, func() { h.unsubscribe(id) }
}

func (h *Hub) unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
}

// Publish menyiarkan event. Non-blocking: subscriber lambat yang penuh dilewati (drop),
// agar satu konsumen macet tidak memblokir state machine (P3/§5.6).
func (h *Hub) Publish(name string, data map[string]any) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	e := Event{Name: name, Data: data}
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Count mengembalikan jumlah subscriber aktif (untuk /health & test).
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Emit mengembalikan closure yang cocok dengan signature Emit pada gate controller.
func (h *Hub) Emit() func(string, map[string]any) {
	return func(name string, data map[string]any) { h.Publish(name, data) }
}
