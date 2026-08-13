package gate

import (
	"context"
	"errors"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/fare"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// State machine gerbang KELUAR (PRD §5.5). Aturan interlock & anti-tailgating identik §5.4.1.

type XState string

const (
	XIdle             XState = "IDLE"
	XIdentifying      XState = "IDENTIFYING"
	XTransactionFound XState = "TRANSACTION_FOUND"
	XDisputeHold      XState = "DISPUTE_HOLD"
	XFareCalculated   XState = "FARE_CALCULATED"
	XAwaitingPayment  XState = "AWAITING_PAYMENT"
	XPaid             XState = "PAID"
	XOpening          XState = "OPENING"
	XOpen             XState = "OPEN"
	XClearing         XState = "CLEARING"
	XClosing          XState = "CLOSING"
	XFault            XState = "FAULT"
)

// Metode pembayaran (PRD §6.1 / §11.2).
const (
	MethodCash      = "CASH"
	MethodEDCEmoney = "EDC_EMONEY"
	MethodEDCDebit  = "EDC_DEBIT"
	MethodQRIS      = "QRIS"
	MethodEwallet   = "EWALLET"
	MethodMember    = "MEMBER"
)

type XEventKind int

const (
	XEvLD3 XEventKind = iota
	XEvLD4
	XEvIdentifyTicket
	XEvIdentifyRFID
	XEvIdentifyPlate
	XEvLostTicket
	XEvPhotoVerdict
	XEvPaymentSelect
	XEvOverride
	XEvTimeout
)

type XEvent struct {
	Kind     XEventKind
	High     bool
	Ticket   string
	UID      string
	Plate    string
	Match    bool   // untuk PhotoVerdict
	Method   string // untuk PaymentSelect
	Tendered int64  // untuk CASH
	Reason   string // untuk Timeout
}

const (
	xtoIdentify = "identify"
	xtoPayment  = "payment"
	xtoNoShow   = "no_show_exit"
)

// ── dependency interfaces ──

type LookupKey struct {
	Ticket string
	UID    string
	Plate  string
}

// ExitTx — hasil pencarian transaksi masuk untuk kendaraan yang akan keluar.
type ExitTx struct {
	ID               string
	VehicleType      string
	EntryTime        time.Time
	MembershipActive bool // member aktif & mencakup site → tarif 0
	ImgInKey         string
	Found            bool
}

type ExitStore interface {
	Lookup(ctx context.Context, key LookupKey) (ExitTx, error)
	Complete(ctx context.Context, txID string, amount int64, plateOut string, flags []string) error
	MarkDispute(ctx context.Context, txID string) error
}

type RateCard struct {
	BaseRate      int64
	FirstHourRate int64
}

type Tariffs interface {
	Resolve(vehicleType string) (RateCard, bool)
}

// SettleInfo — data hasil pembayaran untuk disimpan (§6.2.1 masked PAN).
type SettleInfo struct {
	Tendered, ChangeGiven, BalanceAfter        int64
	ApprovalCode, BatchNo, MaskedPAN, CardType string
}

type Payments interface {
	Begin(ctx context.Context, txID, method string, amount int64) (payID string, err error)
	Settle(ctx context.Context, payID string, info SettleInfo) error
	Fail(ctx context.Context, payID, reason string) error
}

// SiteConfig — konfigurasi tarif site (PRD §11.2 sites).
type SiteConfig struct {
	GraceMinutes       int
	PeakWindows        []fare.PeakWindow
	PeakMultiplierX100 int64
	MaxDailyRate       int64
	LostTicketPenalty  int64
}

type XTimeouts struct {
	Identify time.Duration // 15s
	Payment  time.Duration // 180s
	NoShow   time.Duration // 45s
}

func (t XTimeouts) withDefaults() XTimeouts {
	if t.Identify == 0 {
		t.Identify = 15 * time.Second
	}
	if t.Payment == 0 {
		t.Payment = 180 * time.Second
	}
	if t.NoShow == 0 {
		t.NoShow = 45 * time.Second
	}
	return t
}

type ExitDeps struct {
	Barrier  hw.Barrier
	Light    hw.IndicatorLight
	Terminal hw.PaymentTerminal
	Store    ExitStore
	Payments Payments
	Tariffs  Tariffs
	Auditor  Auditor
	Site     SiteConfig
	Timeouts XTimeouts

	NodeID, TenantID, SiteID, GateLabel string

	ArmTimeout  func(d time.Duration, reason string)
	CancelTimer func()
	Emit        func(name string, data map[string]any)
	Online      func() bool // true = online (QRIS/e-wallet aktif)
	Now         func() time.Time
}

type ExitController struct {
	d        ExitDeps
	state    XState
	cur      ExitTx
	payID    string
	amount   int64
	flags    []string
	payFails int
	ld3High  bool
	ld4High  bool
}

func NewExitController(d ExitDeps) *ExitController {
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
	if d.Online == nil {
		d.Online = func() bool { return true }
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &ExitController{d: d, state: XIdle}
}

func (c *ExitController) State() XState { return c.state }
func (c *ExitController) Amount() int64 { return c.amount }

func (c *ExitController) transition(to XState) {
	from := c.state
	c.state = to
	c.d.Emit("gate.state_changed", map[string]any{"gate": "exit", "from": string(from), "to": string(to)})
}

func (c *ExitController) Handle(ctx context.Context, e XEvent) error {
	switch e.Kind {
	case XEvLD3:
		c.ld3High = e.High
	case XEvLD4:
		c.ld4High = e.High
		if e.High && c.state != XOpen && c.state != XClearing {
			c.audit(ctx, "UNAUTHORIZED_PASSAGE", audit.SevCritical, "LD4 naik tanpa otorisasi", nil)
			c.d.Emit("alert.raised", map[string]any{"type": "UNAUTHORIZED_PASSAGE", "severity": "critical"})
		}
	}

	switch c.state {
	case XIdle:
		return c.onIdle(ctx, e)
	case XIdentifying:
		return c.onIdentifying(ctx, e)
	case XTransactionFound:
		return c.onTransactionFound(ctx, e)
	case XDisputeHold:
		return c.onDisputeHold(ctx, e)
	case XFareCalculated:
		return c.onFareCalculated(ctx, e)
	case XAwaitingPayment:
		return c.onAwaitingPayment(ctx, e)
	case XOpen:
		return c.onOpen(ctx, e)
	case XClearing:
		return c.onClearing(ctx, e)
	}
	return nil
}

func (c *ExitController) onIdle(ctx context.Context, e XEvent) error {
	if e.Kind == XEvLD3 && e.High {
		c.reset()
		c.transition(XIdentifying)
		_ = c.d.Light.Set(ctx, hw.PatternRed)
		c.d.ArmTimeout(c.d.Timeouts.Identify, xtoIdentify)
		c.d.Emit("vehicle.detected", map[string]any{"gate": "exit"})
		// Snapshot LPR keluar dipicu async oleh driver (P2/§7.3).
	}
	return nil
}

func (c *ExitController) onIdentifying(ctx context.Context, e XEvent) error {
	switch e.Kind {
	case XEvIdentifyTicket:
		return c.lookup(ctx, LookupKey{Ticket: e.Ticket})
	case XEvIdentifyRFID:
		return c.lookup(ctx, LookupKey{UID: e.UID})
	case XEvIdentifyPlate:
		return c.lookup(ctx, LookupKey{Plate: e.Plate})
	case XEvLostTicket:
		return c.handleLostTicket(ctx, e.Plate)
	case XEvTimeout:
		if e.Reason == xtoIdentify {
			// Tak ada identifikasi → butuh konfirmasi kasir via plate lookup; tetap IDENTIFYING,
			// emit agar POS memunculkan panel input manual.
			c.d.Emit("exit.identify_timeout", map[string]any{"gate": "exit"})
		}
	}
	return nil
}

func (c *ExitController) lookup(ctx context.Context, key LookupKey) error {
	tx, err := c.d.Store.Lookup(ctx, key)
	if err != nil {
		return c.fault(ctx, err)
	}
	c.d.CancelTimer()
	if !tx.Found {
		// Kendaraan tidak ada di database → tarif "unregistered" = max harian, audit critical
		// (mengindikasikan gerbang masuk pernah ditembus, §5.5.1).
		c.cur = ExitTx{ID: "", Found: false, VehicleType: "mobil"}
		c.flags = append(c.flags, "unregistered_entry")
		c.amount = c.d.Site.MaxDailyRate
		c.audit(ctx, "UNREGISTERED_EXIT", audit.SevCritical, "kendaraan tak terdaftar keluar", nil)
		c.transition(XFareCalculated)
		c.emitFare(ctx, fare.Result{Amount: c.amount, Reason: "unregistered"})
		return nil
	}
	c.cur = tx
	c.transition(XTransactionFound)
	// Hitung tarif agar POS menampilkan sebelum komparasi foto.
	res := c.computeFare(false)
	c.amount = res.Amount
	c.d.Emit("exit.transaction_found", map[string]any{
		"tx": tx.ID, "amount": res.Amount, "img_in": tx.ImgInKey, "duration_min": res.DurationMin,
	})
	return nil
}

func (c *ExitController) onTransactionFound(ctx context.Context, e XEvent) error {
	if e.Kind != XEvPhotoVerdict {
		return nil
	}
	if !e.Match {
		// Foto tidak cocok → tahan, butuh override SuperAdmin (§5.5).
		c.transition(XDisputeHold)
		c.flags = append(c.flags, "photo_mismatch")
		if c.cur.ID != "" {
			_ = c.d.Store.MarkDispute(ctx, c.cur.ID)
		}
		c.audit(ctx, "PHOTO_MISMATCH", audit.SevCritical, "komparasi foto tidak cocok", map[string]any{"tx": c.cur.ID})
		c.d.Emit("alert.raised", map[string]any{"type": "PHOTO_MISMATCH", "severity": "critical"})
		return nil
	}
	return c.toFareCalculated(ctx)
}

func (c *ExitController) onDisputeHold(ctx context.Context, e XEvent) error {
	if e.Kind == XEvOverride {
		c.audit(ctx, "DISPUTE_OVERRIDE", audit.SevCritical, "override SuperAdmin atas dispute foto", map[string]any{"tx": c.cur.ID})
		return c.toFareCalculated(ctx)
	}
	return nil
}

func (c *ExitController) toFareCalculated(ctx context.Context) error {
	return c.presentFare(ctx, c.computeFare(false))
}

// presentFare menyimpan tarif lalu memutuskan: gratis → langsung buka; berbayar → tunggu kasir
// memilih metode di FARE_CALCULATED (§5.5: member aktif → Rp0 → langsung OPENING).
func (c *ExitController) presentFare(ctx context.Context, res fare.Result) error {
	c.amount = res.Amount
	if c.amount == 0 {
		method := ""
		if c.cur.MembershipActive {
			method = MethodMember
		}
		return c.markPaidAndOpen(ctx, method)
	}
	c.transition(XFareCalculated)
	c.emitFare(ctx, res)
	return nil
}

func (c *ExitController) onFareCalculated(ctx context.Context, e XEvent) error {
	// Kasir memilih metode → masuk AWAITING_PAYMENT (arm timeout 180s) lalu proses.
	if e.Kind == XEvPaymentSelect {
		c.transition(XAwaitingPayment)
		c.d.ArmTimeout(c.d.Timeouts.Payment, xtoPayment)
		return c.processPayment(ctx, e.Method, e.Tendered)
	}
	return nil
}

func (c *ExitController) onAwaitingPayment(ctx context.Context, e XEvent) error {
	switch {
	case e.Kind == XEvTimeout && e.Reason == xtoPayment:
		c.transition(XFareCalculated)
		c.d.Emit("exit.payment_timeout", nil)
		return nil
	case e.Kind == XEvPaymentSelect:
		return c.processPayment(ctx, e.Method, e.Tendered)
	}
	return nil
}

func (c *ExitController) processPayment(ctx context.Context, method string, tendered int64) error {
	// QRIS & e-wallet tidak tersedia saat offline (§6.1).
	if (method == MethodQRIS || method == MethodEwallet) && !c.d.Online() {
		c.d.Emit("exit.method_unavailable", map[string]any{"method": method, "reason": "offline"})
		return nil
	}

	payID, err := c.d.Payments.Begin(ctx, c.cur.ID, method, c.amount) // PENDING dulu (§6.2.3 #1)
	if err != nil {
		return c.fault(ctx, err)
	}
	c.payID = payID

	switch method {
	case MethodCash:
		if tendered < c.amount {
			_ = c.d.Payments.Fail(ctx, payID, "insufficient_cash")
			c.d.Emit("exit.cash_insufficient", map[string]any{"amount": c.amount, "tendered": tendered})
			return nil // tetap AWAITING_PAYMENT
		}
		change := tendered - c.amount
		_ = c.d.Payments.Settle(ctx, payID, SettleInfo{Tendered: tendered, ChangeGiven: change})
		return c.markPaidAndOpen(ctx, method)

	case MethodEDCEmoney, MethodEDCDebit:
		return c.processEDC(ctx, payID, method)

	case MethodMember:
		_ = c.d.Payments.Settle(ctx, payID, SettleInfo{})
		return c.markPaidAndOpen(ctx, method)

	case MethodQRIS, MethodEwallet:
		// Online: mint QR via Cloud dilakukan driver; di sini biarkan PENDING menunggu webhook.
		c.d.Emit("exit.qr_pending", map[string]any{"pay": payID, "method": method})
		return nil
	}
	_ = c.d.Payments.Fail(ctx, payID, "unknown_method")
	return nil
}

// processEDC: aturan D8 — timeout ≠ gagal; wajib LastBatch untuk cegah double-charge (§6.2.3 #2).
func (c *ExitController) processEDC(ctx context.Context, payID, method string) error {
	cardType := hw.CardEmoney
	if method == MethodEDCDebit {
		cardType = hw.CardDebit
	}
	res, err := c.d.Terminal.Charge(ctx, c.amount, cardType)
	if errors.Is(err, hw.ErrTerminalTimeout) {
		// Jangan simpulkan gagal. Cek batch terminal.
		if _, bErr := c.d.Terminal.LastBatch(ctx); bErr == nil {
			// Biarkan payment PENDING untuk rekonsiliasi manual; JANGAN charge ulang.
			c.audit(ctx, "EDC_TIMEOUT_RECONCILE", audit.SevWarning,
				"EDC timeout, ditinggalkan PENDING untuk rekonsiliasi (cegah double-charge)",
				map[string]any{"pay": payID})
			c.d.Emit("exit.edc_reconcile", map[string]any{"pay": payID})
		}
		return nil // tetap AWAITING_PAYMENT; kasir dapat cek struk / coba metode lain
	}
	if err != nil {
		return c.fault(ctx, err)
	}
	if !res.Approved {
		return c.declinePayment(ctx, payID, "edc_declined")
	}
	_ = c.d.Payments.Settle(ctx, payID, SettleInfo{
		BalanceAfter: res.BalanceAfter, ApprovalCode: res.ApprovalCode,
		BatchNo: res.BatchNo, MaskedPAN: res.MaskedPAN, CardType: string(res.CardType),
	})
	return c.markPaidAndOpen(ctx, method)
}

func (c *ExitController) declinePayment(ctx context.Context, payID, reason string) error {
	_ = c.d.Payments.Fail(ctx, payID, reason)
	c.payFails++
	c.audit(ctx, "PAYMENT_FAILED", audit.SevWarning, reason, map[string]any{"attempt": c.payFails})
	if c.payFails >= 3 {
		// 3× gagal → kembali FARE_CALCULATED, kasir tawarkan metode lain (§5.5.1).
		c.payFails = 0
		c.transition(XFareCalculated)
		c.d.Emit("exit.payment_retry_exhausted", nil)
		return nil
	}
	c.d.Emit("exit.payment_failed", map[string]any{"attempt": c.payFails})
	return nil // tetap AWAITING_PAYMENT
}

func (c *ExitController) markPaidAndOpen(ctx context.Context, method string) error {
	c.d.CancelTimer()
	c.transition(XPaid)
	if c.cur.ID != "" {
		if err := c.d.Store.Complete(ctx, c.cur.ID, c.amount, "", c.flags); err != nil {
			return c.fault(ctx, err)
		}
	}
	c.audit(ctx, "VEHICLE_EXITED", audit.SevNormal, "kendaraan keluar",
		map[string]any{"tx": c.cur.ID, "amount": c.amount, "method": method})
	c.d.Emit("payment.settled", map[string]any{"tx": c.cur.ID, "amount": c.amount, "method": method})
	c.d.Emit("vehicle.exited", map[string]any{"tx": c.cur.ID})

	// OPENING → OPEN.
	c.transition(XOpening)
	if err := c.d.Barrier.Open(ctx); err != nil {
		return c.fault(ctx, err)
	}
	_ = c.d.Light.Set(ctx, hw.PatternGreen)
	c.transition(XOpen)
	c.d.ArmTimeout(c.d.Timeouts.NoShow, xtoNoShow)
	return nil
}

func (c *ExitController) onOpen(ctx context.Context, e XEvent) error {
	switch {
	case e.Kind == XEvLD4 && e.High:
		c.d.CancelTimer()
		c.transition(XClearing)
	case e.Kind == XEvTimeout && e.Reason == xtoNoShow:
		c.audit(ctx, "NO_SHOW", audit.SevWarning, "palang keluar buka tanpa kendaraan lewat", nil)
		return c.closeGate(ctx)
	}
	return nil
}

func (c *ExitController) onClearing(ctx context.Context, e XEvent) error {
	if e.Kind == XEvLD4 && !e.High {
		return c.closeGate(ctx)
	}
	return nil
}

func (c *ExitController) closeGate(ctx context.Context) error {
	if err := c.d.Barrier.Close(ctx); err != nil {
		c.transition(XClearing) // interlock menolak (loop bawah HIGH) → coba lagi saat LD4 falling
		return nil
	}
	_ = c.d.Light.Set(ctx, hw.PatternRed)
	// Anti-tailgating (§5.4.1, identik): bila LD3 masih HIGH → mulai identifikasi kendaraan kedua.
	if c.ld3High {
		c.reset()
		c.transition(XIdentifying)
		c.d.ArmTimeout(c.d.Timeouts.Identify, xtoIdentify)
		c.audit(ctx, "ANTITAILGATE_NEW_CYCLE", audit.SevNormal, "kendaraan kedua keluar, siklus baru", nil)
		return nil
	}
	c.transition(XIdle)
	return nil
}

func (c *ExitController) handleLostTicket(ctx context.Context, plate string) error {
	c.d.CancelTimer()
	// Tiket hilang → tarif max harian + denda; wajib plat manual (§5.5.1). Foto KTP/STNK di POS.
	rc, _ := c.d.Tariffs.Resolve("mobil")
	res := fare.Calculate(fare.Input{
		LostTicket: true, MaxDailyRate: c.d.Site.MaxDailyRate,
		LostTicketPenalty: c.d.Site.LostTicketPenalty, BaseRate: rc.BaseRate,
	})
	c.amount = res.Amount
	c.flags = append(c.flags, "lost_ticket")
	c.cur = ExitTx{Found: false}
	c.audit(ctx, "LOST_TICKET", audit.SevWarning, "tiket hilang, plat manual",
		map[string]any{"plate": plate, "amount": c.amount})
	c.transition(XFareCalculated)
	c.emitFare(ctx, res)
	return nil
}

// computeFare menghitung tarif untuk transaksi saat ini pada waktu keluar sekarang.
func (c *ExitController) computeFare(lost bool) fare.Result {
	rc, _ := c.d.Tariffs.Resolve(c.cur.VehicleType)
	return fare.Calculate(fare.Input{
		EntryTime:          c.cur.EntryTime,
		ExitTime:           c.d.Now(),
		GraceMinutes:       c.d.Site.GraceMinutes,
		MemberCoversSite:   c.cur.MembershipActive,
		BaseRate:           rc.BaseRate,
		FirstHourRate:      rc.FirstHourRate,
		PeakMultiplierX100: c.d.Site.PeakMultiplierX100,
		PeakWindows:        c.d.Site.PeakWindows,
		MaxDailyRate:       c.d.Site.MaxDailyRate,
		LostTicket:         lost,
		LostTicketPenalty:  c.d.Site.LostTicketPenalty,
	})
}

func (c *ExitController) emitFare(ctx context.Context, res fare.Result) {
	c.d.Emit("exit.fare_calculated", map[string]any{
		"amount": res.Amount, "duration_min": res.DurationMin, "reason": res.Reason,
	})
}

func (c *ExitController) reset() {
	c.cur = ExitTx{}
	c.payID = ""
	c.amount = 0
	c.flags = nil
	c.payFails = 0
}

func (c *ExitController) fault(ctx context.Context, err error) error {
	c.transition(XFault)
	_ = c.d.Light.Set(ctx, hw.PatternRedBlink)
	c.audit(ctx, "FAULT", audit.SevCritical, err.Error(), nil)
	return err
}

func (c *ExitController) audit(ctx context.Context, evType string, sev audit.Severity, summary string, payload map[string]any) {
	if c.d.Auditor == nil {
		return
	}
	_ = c.d.Auditor.Record(ctx, audit.Event{
		TenantID: c.d.TenantID, SiteID: c.d.SiteID, EventType: evType, Severity: sev,
		ActorLabel: "System", ActorRole: "System", GateLabel: c.d.GateLabel,
		Summary: summary, Payload: payload, CreatedAt: c.d.Now(),
	})
}
