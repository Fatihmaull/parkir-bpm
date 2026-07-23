// Package gate — state machine gerbang (PRD §5.4/§5.5). Ini Layer 3 (§5.2): bahasa domain.
// State machine berjalan di goroutine sendiri (§5.6) dan tidak pernah menulis byte langsung —
// hanya lewat interface hardware (Layer 2). Semua transisi deterministik & dapat diuji terhadap
// simulator tanpa perangkat fisik (P7).
package gate

import (
	"context"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

type State string

const (
	StateIdle           State = "IDLE"
	StateVehiclePresent State = "VEHICLE_PRESENT"
	StateIssuing        State = "ISSUING"
	StateAwaitingPull   State = "AWAITING_PULL"
	StateRejected       State = "REJECTED"
	StateOpening        State = "OPENING"
	StateOpen           State = "OPEN"
	StateClearing       State = "CLEARING"
	StateClosing        State = "CLOSING"
	StateFault          State = "FAULT"
	StateLockedNoPaper  State = "LOCKED_NO_PAPER"
)

// EventKind — jenis peristiwa yang menggerakkan state machine.
type EventKind int

const (
	EvLD1 EventKind = iota // loop sebelum palang (kehadiran)
	EvLD2                  // loop di bawah/setelah palang (interlock + anti-tailgating)
	EvButton
	EvRFID
	EvTicketTaken
	EvTimeout
)

// Event — peristiwa masuk. High untuk LD1/LD2; UID untuk RFID; Reason untuk Timeout.
type Event struct {
	Kind   EventKind
	High   bool
	UID    string
	Reason string
}

// Timeout reasons.
const (
	toPull   = "ticket_pull"
	toNoShow = "no_show"
	toReject = "reject_display"
)

// ── Dependency interfaces (di-fake saat test) ──

type Store interface {
	CreateDraft(ctx context.Context) (txID string, err error)
	CommitInPremises(ctx context.Context, txID, ticketCode, plate string, membershipID string) error
	Void(ctx context.Context, txID, reason string) error
}

type Members interface {
	ValidateEntry(ctx context.Context, uid string) (MemberDecision, error)
}

type MemberDecision struct {
	Allowed      bool
	MembershipID string
	Reason       string // mis. ANTIPASSBACK_VIOLATION, EXPIRED
}

type Auditor interface {
	Record(ctx context.Context, e audit.Event) error
}

type TicketGen interface {
	Next() (ticketCode, qrPayload string)
}

// Timeouts (PRD §5.4.2). Nilai 0 memakai default.
type Timeouts struct {
	TicketPull time.Duration // 30s
	NoShow     time.Duration // 45s
	Reject     time.Duration // 3s
}

func (t Timeouts) withDefaults() Timeouts {
	if t.TicketPull == 0 {
		t.TicketPull = 30 * time.Second
	}
	if t.NoShow == 0 {
		t.NoShow = 45 * time.Second
	}
	if t.Reject == 0 {
		t.Reject = 3 * time.Second
	}
	return t
}

// Deps mengumpulkan seluruh dependensi controller.
type Deps struct {
	Barrier  hw.Barrier
	Light    hw.IndicatorLight
	Printer  hw.TicketPrinter
	Store    Store
	Members  Members
	Auditor  Auditor
	Tickets  TicketGen
	Timeouts Timeouts

	NodeID    string
	TenantID  string
	SiteID    string
	GateLabel string

	// ArmTimeout dipanggil untuk menjadwalkan Event{Kind:EvTimeout,Reason:...} setelah d.
	// Driver produksi memasang timer nyata; test memanggil balik secara manual.
	ArmTimeout  func(d time.Duration, reason string)
	CancelTimer func()
	// Emit memancarkan event WS (§5.6). Boleh nil.
	Emit func(name string, data map[string]any)
}

// Controller — state machine gerbang masuk.
type Controller struct {
	d         Deps
	state     State
	txID      string
	curTicket string
	// snapshot loop terakhir untuk keputusan anti-tailgating & interlock.
	ld1High bool
	ld2High bool
}

func NewController(d Deps) *Controller {
	d.Timeouts = d.Timeouts.withDefaults()
	if d.Emit == nil {
		d.Emit = func(string, map[string]any) {}
	}
	if d.ArmTimeout == nil {
		d.ArmTimeout = func(time.Duration, string) {}
	}
	if d.CancelTimer == nil {
		d.CancelTimer = func() {}
	}
	return &Controller{d: d, state: StateIdle}
}

func (c *Controller) State() State { return c.state }

func (c *Controller) transition(to State) {
	from := c.state
	c.state = to
	c.d.Emit("gate.state_changed", map[string]any{"from": string(from), "to": string(to)})
}

// Handle memproses satu event. Bounded — tidak pernah memblokir selain pada panggilan
// hardware Layer-2 yang punya timeout sendiri.
func (c *Controller) Handle(ctx context.Context, e Event) error {
	// Pelacakan state loop global (dipakai lintas-state).
	switch e.Kind {
	case EvLD1:
		c.ld1High = e.High
	case EvLD2:
		c.ld2High = e.High
		// Palang ditembus: LD2 naik padahal bukan state OPEN/CLEARING → UNAUTHORIZED_PASSAGE.
		if e.High && c.state != StateOpen && c.state != StateClearing {
			c.audit(ctx, "UNAUTHORIZED_PASSAGE", audit.SevCritical, "LD2 naik tanpa otorisasi", nil)
			c.d.Emit("alert.raised", map[string]any{"type": "UNAUTHORIZED_PASSAGE", "severity": "critical"})
		}
	}

	switch c.state {
	case StateIdle:
		return c.onIdle(ctx, e)
	case StateVehiclePresent:
		return c.onVehiclePresent(ctx, e)
	case StateAwaitingPull:
		return c.onAwaitingPull(ctx, e)
	case StateOpen:
		return c.onOpen(ctx, e)
	case StateClearing:
		return c.onClearing(ctx, e)
	case StateRejected:
		return c.onRejected(ctx, e)
	}
	return nil
}

func (c *Controller) onIdle(ctx context.Context, e Event) error {
	if e.Kind == EvLD1 && e.High {
		txID, err := c.d.Store.CreateDraft(ctx)
		if err != nil {
			return c.fault(ctx, err)
		}
		c.txID = txID
		c.transition(StateVehiclePresent)
		_ = c.d.Light.Set(ctx, hw.PatternRed) // sedang diproses
		c.d.Emit("vehicle.detected", map[string]any{"tx": txID})
		// LPR dipicu async oleh driver; hasil tidak menggerbang keputusan (P2/§7.3).
	}
	return nil
}

func (c *Controller) onVehiclePresent(ctx context.Context, e Event) error {
	switch e.Kind {
	case EvRFID:
		return c.handleMember(ctx, e.UID)
	case EvButton:
		return c.issueTicket(ctx)
	}
	return nil
}

func (c *Controller) issueTicket(ctx context.Context) error {
	st, _ := c.d.Printer.Status(ctx)
	if st == hw.PrinterPaperOut || st == hw.PrinterJam {
		// Kertas habis: casual berhenti (LOCKED_NO_PAPER), member tetap bisa (§5.4.3).
		c.transition(StateLockedNoPaper)
		_ = c.d.Light.Set(ctx, hw.PatternRedBlink)
		c.audit(ctx, "LOCKED_NO_PAPER", audit.SevCritical, "printer kehabisan kertas", nil)
		return nil
	}
	c.transition(StateIssuing)
	code, qr := c.d.Tickets.Next()
	if err := c.d.Printer.Print(ctx, hw.TicketPayload{TicketCode: code, QRPayload: qr}); err != nil {
		return c.fault(ctx, err)
	}
	c.curTicket = code
	c.transition(StateAwaitingPull)
	c.d.ArmTimeout(c.d.Timeouts.TicketPull, toPull)
	return nil
}

func (c *Controller) onAwaitingPull(ctx context.Context, e Event) error {
	switch {
	case e.Kind == EvTicketTaken:
		c.d.CancelTimer()
		return c.openForEntry(ctx, c.curTicket, "")
	case e.Kind == EvTimeout && e.Reason == toPull:
		// Tiket tak diambil → retract, VOID (§5.4.2).
		_ = c.Void(ctx)
		c.transition(StateIdle)
		_ = c.d.Light.Set(ctx, hw.PatternRed)
	}
	return nil
}

func (c *Controller) handleMember(ctx context.Context, uid string) error {
	dec, err := c.d.Members.ValidateEntry(ctx, uid)
	if err != nil {
		return c.fault(ctx, err)
	}
	if !dec.Allowed {
		c.transition(StateRejected)
		_ = c.d.Light.Set(ctx, hw.PatternRedBlink)
		c.audit(ctx, dec.Reason, audit.SevWarning, "tap member ditolak", map[string]any{"uid": uid})
		c.d.ArmTimeout(c.d.Timeouts.Reject, toReject)
		return nil
	}
	return c.openForEntry(ctx, "", dec.MembershipID)
}

// openForEntry: commit IN_PREMISES + buka palang + lampu hijau (state OPENING→OPEN).
func (c *Controller) openForEntry(ctx context.Context, ticketCode, membershipID string) error {
	c.transition(StateOpening)
	if err := c.d.Store.CommitInPremises(ctx, c.txID, ticketCode, "", membershipID); err != nil {
		return c.fault(ctx, err)
	}
	if err := c.d.Barrier.Open(ctx); err != nil {
		return c.fault(ctx, err)
	}
	_ = c.d.Light.Set(ctx, hw.PatternGreen)
	sev := audit.SevNormal
	c.audit(ctx, "VEHICLE_ENTERED", sev, "kendaraan masuk", map[string]any{"tx": c.txID})
	c.d.Emit("vehicle.entered", map[string]any{"tx": c.txID})
	c.transition(StateOpen)
	c.d.ArmTimeout(c.d.Timeouts.NoShow, toNoShow)
	return nil
}

func (c *Controller) onOpen(ctx context.Context, e Event) error {
	switch {
	case e.Kind == EvLD2 && e.High:
		// Kendaraan mulai lewat.
		c.d.CancelTimer()
		c.transition(StateClearing)
	case e.Kind == EvTimeout && e.Reason == toNoShow:
		// Tak ada yang lewat → tutup, tetap IN_PREMISES, tandai no_show (§5.4.2).
		c.audit(ctx, "NO_SHOW", audit.SevWarning, "palang buka tanpa kendaraan lewat", nil)
		return c.closeGate(ctx)
	}
	return nil
}

func (c *Controller) onClearing(ctx context.Context, e Event) error {
	// Interlock (§5.4.1): tutup hanya setelah LD2 falling.
	if e.Kind == EvLD2 && !e.High {
		return c.closeGate(ctx)
	}
	return nil
}

// closeGate menutup palang (interlock ditegakkan di Barrier.Close) lalu memutuskan
// IDLE vs siklus baru (anti-tailgating §5.4.1).
func (c *Controller) closeGate(ctx context.Context) error {
	if err := c.d.Barrier.Close(ctx); err != nil {
		// Interlock menolak (loop bawah HIGH) → tetap di CLEARING, coba lagi saat LD2 falling.
		c.transition(StateClearing)
		return nil
	}
	_ = c.d.Light.Set(ctx, hw.PatternRed)
	c.txID = ""
	c.curTicket = ""
	// Anti-tailgating: bila LD1 masih HIGH, mobil kedua wajib ambil tiket sendiri → siklus baru.
	if c.ld1High {
		txID, err := c.d.Store.CreateDraft(ctx)
		if err != nil {
			return c.fault(ctx, err)
		}
		c.txID = txID
		c.transition(StateVehiclePresent)
		c.audit(ctx, "ANTITAILGATE_NEW_CYCLE", audit.SevNormal, "kendaraan kedua, siklus baru", nil)
		return nil
	}
	c.transition(StateIdle)
	return nil
}

func (c *Controller) onRejected(ctx context.Context, e Event) error {
	if e.Kind == EvTimeout && e.Reason == toReject {
		c.transition(StateIdle)
		_ = c.d.Light.Set(ctx, hw.PatternRed)
	}
	return nil
}

// ── helper ──

func (c *Controller) fault(ctx context.Context, err error) error {
	c.transition(StateFault)
	_ = c.d.Light.Set(ctx, hw.PatternRedBlink)
	c.audit(ctx, "FAULT", audit.SevCritical, err.Error(), nil)
	c.d.Emit("alert.raised", map[string]any{"type": "FAULT", "severity": "critical"})
	return err
}

func (c *Controller) audit(ctx context.Context, evType string, sev audit.Severity, summary string, payload map[string]any) {
	if c.d.Auditor == nil {
		return
	}
	_ = c.d.Auditor.Record(ctx, audit.Event{
		TenantID: c.d.TenantID, SiteID: c.d.SiteID, EventType: evType, Severity: sev,
		ActorLabel: "System", ActorRole: "System", GateLabel: c.d.GateLabel,
		Summary: summary, Payload: payload, CreatedAt: time.Now(),
	})
}

func (c *Controller) Void(ctx context.Context) error {
	if c.txID == "" {
		return nil
	}
	err := c.d.Store.Void(ctx, c.txID, "ticket_not_taken")
	c.audit(ctx, "TICKET_VOID", audit.SevWarning, "tiket tidak diambil, dibatalkan", map[string]any{"tx": c.txID})
	c.txID = ""
	c.curTicket = ""
	return err
}
