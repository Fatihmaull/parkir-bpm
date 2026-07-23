package sim

import (
	"context"
	"sync"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// ── Loop detector ──

type Loop struct {
	mu   sync.Mutex
	high bool
	subs []chan hw.LoopEvent
	id   int
}

func NewLoop() *Loop { return &Loop{} }

func (l *Loop) Read(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.high, nil
}

func (l *Loop) Subscribe() <-chan hw.LoopEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := make(chan hw.LoopEvent, 8)
	l.subs = append(l.subs, ch)
	return ch
}

// isHigh dipakai sebagai callback interlock oleh Barrier.
func (l *Loop) isHigh() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.high
}

// Drive menyetel state loop (mensimulasikan kendaraan). Mengirim event ke subscriber
// bila state berubah — sinyal dianggap sudah ter-debounce (kontrak §5.3.4 #3).
func (l *Loop) Drive(high bool) {
	l.mu.Lock()
	if l.high == high {
		l.mu.Unlock()
		return
	}
	l.high = high
	evt := hw.LoopEvent{LoopID: l.id, High: high, TS: time.Now().UnixMilli()}
	subs := append([]chan hw.LoopEvent(nil), l.subs...)
	l.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- evt:
		default: // jangan blokir simulator bila konsumen lambat
		}
	}
}

// ── Ticket printer ──

type Printer struct {
	mu     sync.Mutex
	status hw.PrinterStatus
	subs   []chan hw.PrinterEvent
}

func NewPrinter() *Printer { return &Printer{status: hw.PrinterOK} }

func (p *Printer) Print(ctx context.Context, t hw.TicketPayload) error {
	p.mu.Lock()
	st := p.status
	p.mu.Unlock()
	if st == hw.PrinterPaperOut || st == hw.PrinterJam || st == hw.PrinterOffline {
		return hw.ErrPaperOut
	}
	// Setelah cetak, tiket menunggu diambil → emit TICKET_TAKEN saat SimTake dipanggil.
	return nil
}

func (p *Printer) Status(ctx context.Context) (hw.PrinterStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, nil
}

func (p *Printer) Subscribe() <-chan hw.PrinterEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan hw.PrinterEvent, 8)
	p.subs = append(p.subs, ch)
	return ch
}

// SetStatus mensimulasikan perubahan status printer (kertas menipis/habis/jam).
func (p *Printer) SetStatus(s hw.PrinterStatus) {
	p.mu.Lock()
	p.status = s
	subs := append([]chan hw.PrinterEvent(nil), p.subs...)
	p.mu.Unlock()
	kind := map[hw.PrinterStatus]string{
		hw.PrinterPaperLow: "PAPER_LOW", hw.PrinterPaperOut: "PAPER_OUT", hw.PrinterJam: "JAM",
	}[s]
	if kind == "" {
		return
	}
	p.emit(subs, hw.PrinterEvent{Kind: kind, TS: time.Now().UnixMilli()})
}

// SimTake mensimulasikan pengendara mengambil tiket → event TICKET_TAKEN (§5.4 AWAITING_PULL).
func (p *Printer) SimTake() {
	p.mu.Lock()
	subs := append([]chan hw.PrinterEvent(nil), p.subs...)
	p.mu.Unlock()
	p.emit(subs, hw.PrinterEvent{Kind: "TICKET_TAKEN", TS: time.Now().UnixMilli()})
}

func (p *Printer) emit(subs []chan hw.PrinterEvent, e hw.PrinterEvent) {
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// ── RFID reader ──

type RFID struct {
	mu   sync.Mutex
	subs []chan hw.RFIDTap
}

func NewRFID() *RFID { return &RFID{} }

func (r *RFID) Subscribe() <-chan hw.RFIDTap {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan hw.RFIDTap, 8)
	r.subs = append(r.subs, ch)
	return ch
}

// SimTap mensimulasikan kartu Mifare di-tap.
func (r *RFID) SimTap(uid string) {
	r.mu.Lock()
	subs := append([]chan hw.RFIDTap(nil), r.subs...)
	r.mu.Unlock()
	tap := hw.RFIDTap{UID: uid, ReadAt: time.Now().UnixMilli()}
	for _, ch := range subs {
		select {
		case ch <- tap:
		default:
		}
	}
}
