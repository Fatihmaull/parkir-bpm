package sim

import (
	"context"
	"errors"
	"testing"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

func TestBarrierInterlockRefusesCloseWhenLoopHigh(t *testing.T) {
	g := NewGate()
	ctx := context.Background()
	if err := g.Barrier.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}
	// Kendaraan di bawah palang (LD2 HIGH) → tutup HARUS ditolak (P4).
	g.LoopPost.Drive(true)
	if err := g.Barrier.Close(ctx); !errors.Is(err, hw.ErrSafetyInterlock) {
		t.Fatalf("Close saat loop HIGH: want ErrSafetyInterlock, got %v", err)
	}
	st, _ := g.Barrier.State(ctx)
	if st != hw.BarrierOpen {
		t.Fatalf("palang seharusnya tetap OPEN, got %v", st)
	}
	// Kendaraan lewat (LD2 LOW) → tutup boleh.
	g.LoopPost.Drive(false)
	if err := g.Barrier.Close(ctx); err != nil {
		t.Fatalf("Close saat loop LOW: %v", err)
	}
}

func TestBarrierOpenIdempotent(t *testing.T) {
	g := NewGate()
	ctx := context.Background()
	if err := g.Barrier.Open(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.Barrier.Open(ctx); err != nil { // §5.3.4 #5: idempoten
		t.Fatalf("Open kedua harus no-op tanpa error, got %v", err)
	}
}

func TestLoopEventDelivered(t *testing.T) {
	l := NewLoop()
	ch := l.Subscribe()
	l.Drive(true)
	select {
	case e := <-ch:
		if !e.High {
			t.Fatal("event seharusnya High=true")
		}
	case <-time.After(time.Second):
		t.Fatal("tidak menerima LoopEvent")
	}
}

func TestPrinterPaperOutBlocksPrint(t *testing.T) {
	p := NewPrinter()
	ctx := context.Background()
	if err := p.Print(ctx, hw.TicketPayload{TicketCode: "T1"}); err != nil {
		t.Fatalf("cetak normal gagal: %v", err)
	}
	p.SetStatus(hw.PrinterPaperOut)
	if err := p.Print(ctx, hw.TicketPayload{TicketCode: "T2"}); !errors.Is(err, hw.ErrPaperOut) {
		t.Fatalf("want ErrPaperOut, got %v", err)
	}
}

func TestEDCChargeApproved(t *testing.T) {
	e := NewEDC()
	res, err := e.Charge(context.Background(), 5000, hw.CardEmoney)
	if err != nil || !res.Approved {
		t.Fatalf("charge: err=%v approved=%v", err, res.Approved)
	}
	if len(res.MaskedPAN) == 0 || res.ApprovalCode == "" {
		t.Fatal("hasil charge harus punya masked PAN & approval code")
	}
	b, _ := e.LastBatch(context.Background())
	if b.Count != 1 {
		t.Fatalf("batch count = %d, want 1", b.Count)
	}
}

// D8: timeout ≠ gagal. Saat pemanggil timeout tetapi transaksi sebenarnya settle,
// LastBatch() harus memperlihatkannya sehingga logika bisnis tidak double-charge.
func TestEDCTimeoutButSettledVisibleInBatch(t *testing.T) {
	e := NewEDC()
	e.Delay = 100 * time.Millisecond
	e.SettleOnTimeout = true

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := e.Charge(ctx, 7000, hw.CardEmoney)
	if !errors.Is(err, hw.ErrTerminalTimeout) {
		t.Fatalf("want ErrTerminalTimeout, got %v", err)
	}
	// Beri waktu goroutine Charge menuntaskan pencatatan batch.
	time.Sleep(150 * time.Millisecond)
	b, _ := e.LastBatch(context.Background())
	if b.Count != 1 {
		t.Fatalf("transaksi yang timeout-tapi-settle harus tampak di batch; count=%d", b.Count)
	}
}
