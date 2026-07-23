package sim

import (
	"context"
	"fmt"
	"sync"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// EDC — simulator PaymentTerminal (PRD §6.2.1). Mendukung skenario penting untuk uji:
// sukses, tolak, dan TIMEOUT-tetapi-sebenarnya-sukses (untuk membuktikan aturan D8:
// timeout ≠ gagal, pemanggil wajib LastBatch()).
type EDC struct {
	mu         sync.Mutex
	batchNo    string
	traceSeq   int
	batch      []hw.ChargeResult
	terminalID string

	// Kontrol skenario uji:
	Approve      bool          // hasil default Charge
	Delay        time.Duration // tunda respons (untuk memicu timeout pemanggil)
	SettleOnTimeout bool       // bila true: saat pemanggil timeout, transaksi tetap tercatat di batch
}

func NewEDC() *EDC {
	return &EDC{batchNo: "SIMBATCH01", terminalID: "SIMTERM001", Approve: true}
}

func (e *EDC) Charge(ctx context.Context, amount int64, method hw.CardType) (hw.ChargeResult, error) {
	e.mu.Lock()
	e.traceSeq++
	trace := fmt.Sprintf("%06d", e.traceSeq)
	delay := e.Delay
	approve := e.Approve
	settleOnTimeout := e.SettleOnTimeout
	e.mu.Unlock()

	res := hw.ChargeResult{
		Approved:      approve,
		ApprovalCode:  "APPR" + trace,
		CardType:      method,
		MaskedPAN:     "601234******5678", // hanya masked (§6.2.3 #3)
		BalanceBefore: 500000,
		BalanceAfter:  500000 - amount,
		TerminalID:    e.terminalID,
		BatchNo:       e.batchNo,
		TraceNo:       trace,
		RawResponse:   []byte(fmt.Sprintf(`{"trace":"%s","amount":%d}`, trace, amount)),
	}

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			// Pemanggil menyerah. Jika SettleOnTimeout, transaksi TETAP tercatat di terminal →
			// LastBatch() akan memperlihatkannya (membuktikan D8).
			if settleOnTimeout && approve {
				e.recordBatch(res)
			}
			return hw.ChargeResult{}, hw.ErrTerminalTimeout
		}
	}

	if !approve {
		res.Approved = false
		return res, nil
	}
	e.recordBatch(res)
	return res, nil
}

func (e *EDC) recordBatch(r hw.ChargeResult) {
	e.mu.Lock()
	e.batch = append(e.batch, r)
	e.mu.Unlock()
}

func (e *EDC) Cancel(ctx context.Context, ref string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, r := range e.batch {
		if r.TraceNo == ref || r.ApprovalCode == ref {
			e.batch = append(e.batch[:i], e.batch[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("sim edc: ref %q tidak ditemukan di batch", ref)
}

func (e *EDC) LastBatch(ctx context.Context) (hw.BatchSummary, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total int64
	for _, r := range e.batch {
		total += r.BalanceBefore - r.BalanceAfter
	}
	return hw.BatchSummary{BatchNo: e.batchNo, Count: len(e.batch), Total: total}, nil
}

func (e *EDC) Status(ctx context.Context) (hw.TerminalStatus, error) {
	return hw.TerminalReady, nil
}
