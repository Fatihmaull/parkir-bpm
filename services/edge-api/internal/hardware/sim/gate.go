// Package sim menyediakan implementasi simulator untuk seluruh perangkat (prinsip P7, PRD §5.2).
// Simulator ini menegakkan aturan keselamatan yang sama dengan kontrak firmware (§5.3.4) sehingga
// logika Edge dapat dites tanpa perangkat fisik — termasuk interlock keselamatan (P4).
package sim

import (
	"context"
	"sync"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// Gate mengelompokkan perangkat satu gerbang yang saling terkait (barrier, loop, printer, dst.)
// agar interlock keselamatan dapat dibagikan antar-perangkat.
type Gate struct {
	Barrier  *Barrier
	Light    *Light
	Printer  *Printer
	RFID     *RFID
	LoopPre  *Loop // LD1/LD3 — sebelum palang
	LoopPost *Loop // LD2/LD4 — di bawah/­setelah palang (dipakai interlock)
}

// NewGate membangun satu gerbang tersimulasi lengkap.
func NewGate() *Gate {
	post := NewLoop()
	g := &Gate{
		Light:    NewLight(),
		Printer:  NewPrinter(),
		RFID:     NewRFID(),
		LoopPre:  NewLoop(),
		LoopPost: post,
	}
	// Interlock (P4): palang tak boleh menutup selama loop bawah HIGH.
	g.Barrier = NewBarrier(post.isHigh)
	return g
}

// ── Barrier ──

type Barrier struct {
	mu          sync.Mutex
	state       hw.BarrierState
	underIsHigh func() bool // interlock: true bila loop bawah HIGH
	moveDelay   time.Duration
}

func NewBarrier(underIsHigh func() bool) *Barrier {
	return &Barrier{state: hw.BarrierClosed, underIsHigh: underIsHigh, moveDelay: 5 * time.Millisecond}
}

func (b *Barrier) Open(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == hw.BarrierOpen {
		return nil // idempoten (§5.3.4 aturan #5)
	}
	b.state = hw.BarrierMoving
	select {
	case <-time.After(b.moveDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	b.state = hw.BarrierOpen
	return nil
}

func (b *Barrier) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Interlock keselamatan (P4 / §5.3.4 #1): tolak tutup saat loop bawah HIGH.
	if b.underIsHigh != nil && b.underIsHigh() {
		return hw.ErrSafetyInterlock
	}
	if b.state == hw.BarrierClosed {
		return nil // idempoten
	}
	b.state = hw.BarrierMoving
	select {
	case <-time.After(b.moveDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	b.state = hw.BarrierClosed
	return nil
}

func (b *Barrier) State(ctx context.Context) (hw.BarrierState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state, nil
}

// ── Light ──

type Light struct {
	mu      sync.Mutex
	pattern hw.LightPattern
}

func NewLight() *Light { return &Light{pattern: hw.PatternRed} }

func (l *Light) Set(ctx context.Context, p hw.LightPattern) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pattern = p
	return nil
}

func (l *Light) Pattern() hw.LightPattern {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pattern
}
