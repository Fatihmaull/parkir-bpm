// Package hardware mendefinisikan Layer 2 — abstraksi perangkat (PRD §5.2).
// Logika bisnis (Layer 3, gate controller) hanya berbicara lewat interface ini,
// tidak pernah menulis byte langsung. Setiap interface punya dua implementasi:
// produksi (serial/tcp) dan simulator (sim, prinsip P7).
package hardware

import "context"

type BarrierState int

const (
	BarrierUnknown BarrierState = iota
	BarrierClosed
	BarrierOpen
	BarrierMoving
	BarrierFault
)

type LightPattern int

const (
	PatternOff LightPattern = iota
	PatternRed
	PatternGreen
	PatternYellowBlink
	PatternRedBlink
)

type PrinterStatus int

const (
	PrinterOK PrinterStatus = iota
	PrinterPaperLow
	PrinterPaperOut
	PrinterJam
	PrinterOffline
)

type TerminalStatus int

const (
	TerminalReady TerminalStatus = iota
	TerminalBusy
	TerminalOffline
)

// CardType — jenis kartu EDC (PRD §6.2.1).
type CardType string

const (
	CardFlazz   CardType = "FLAZZ"
	CardEmoney  CardType = "EMONEY"
	CardBrizzi  CardType = "BRIZZI"
	CardTapcash CardType = "TAPCASH"
	CardDebit   CardType = "DEBIT"
)

// Event payloads.
type LoopEvent struct {
	LoopID int
	High   bool
	TS     int64 // epoch ms
}

type PrinterEvent struct {
	Kind string // TICKET_TAKEN|PAPER_LOW|PAPER_OUT|JAM
	TS   int64
}

type RFIDTap struct {
	UID    string
	ReadAt int64
}

type TicketPayload struct {
	TicketCode string
	QRPayload  string
	Raw        []byte // ESC/POS jika sudah dirender
}

// ChargeResult — hasil transaksi EDC (PRD §6.2.1). PAN selalu masked.
type ChargeResult struct {
	Approved      bool
	ApprovalCode  string
	CardType      CardType
	MaskedPAN     string // hanya 6 awal + 4 akhir — sisanya JANGAN disimpan
	BalanceBefore int64
	BalanceAfter  int64
	TerminalID    string
	BatchNo       string
	TraceNo       string
	RawResponse   []byte
}

type BatchSummary struct {
	BatchNo string
	Count   int
	Total   int64
}

// ── Layer 2 interfaces (PRD §5.2) ──

type Barrier interface {
	Open(ctx context.Context) error
	Close(ctx context.Context) error
	State(ctx context.Context) (BarrierState, error)
}

type LoopDetector interface {
	Read(ctx context.Context) (bool, error)
	Subscribe() <-chan LoopEvent
}

type IndicatorLight interface {
	Set(ctx context.Context, pattern LightPattern) error
}

type TicketPrinter interface {
	Print(ctx context.Context, t TicketPayload) error
	Status(ctx context.Context) (PrinterStatus, error)
	Subscribe() <-chan PrinterEvent
}

type RFIDReader interface {
	Subscribe() <-chan RFIDTap
}

// PaymentTerminal — abstraksi EDC vendor-agnostik (PRD §6.2.1).
// Timeout ≠ gagal: pemanggil wajib LastBatch() sebelum menyimpulkan kegagalan (D8).
type PaymentTerminal interface {
	Charge(ctx context.Context, amount int64, method CardType) (ChargeResult, error)
	Cancel(ctx context.Context, ref string) error
	LastBatch(ctx context.Context) (BatchSummary, error)
	Status(ctx context.Context) (TerminalStatus, error)
}
