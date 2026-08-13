package gate

import (
	"context"
	"testing"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/sim"
)

// ── fakes ──

type fakeExitStore struct {
	tx        ExitTx
	completed []int64
	disputes  []string
}

func (s *fakeExitStore) Lookup(ctx context.Context, k LookupKey) (ExitTx, error) { return s.tx, nil }
func (s *fakeExitStore) Complete(ctx context.Context, tx string, amt int64, plate string, flags []string) error {
	s.completed = append(s.completed, amt)
	return nil
}
func (s *fakeExitStore) MarkDispute(ctx context.Context, tx string) error {
	s.disputes = append(s.disputes, tx)
	return nil
}

type fakePayments struct {
	n       int
	begun   []string
	settled []string
	failed  []string
}

func (p *fakePayments) Begin(ctx context.Context, tx, method string, amt int64) (string, error) {
	p.n++
	id := "pay-" + method
	p.begun = append(p.begun, method)
	return id, nil
}
func (p *fakePayments) Settle(ctx context.Context, id string, info SettleInfo) error {
	p.settled = append(p.settled, id)
	return nil
}
func (p *fakePayments) Fail(ctx context.Context, id, reason string) error {
	p.failed = append(p.failed, reason)
	return nil
}

type fakeTariffs struct{ base, first int64 }

func (t fakeTariffs) Resolve(vt string) (RateCard, bool) {
	return RateCard{BaseRate: t.base, FirstHourRate: t.first}, true
}

// ── harness ──

type xharness struct {
	c      *ExitController
	g      *sim.Gate
	store  *fakeExitStore
	pay    *fakePayments
	audit  *fakeAudit
	edc    *sim.EDC
	armed  []string
	online bool
	nowT   time.Time
}

func newXHarness(t *testing.T, tx ExitTx) *xharness {
	t.Helper()
	g := sim.NewGate()
	edc := sim.NewEDC()
	entry := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	h := &xharness{
		g: g, store: &fakeExitStore{tx: tx}, pay: &fakePayments{}, audit: &fakeAudit{},
		edc: edc, online: true, nowT: entry.Add(2 * time.Hour), // durasi 2 jam
	}
	d := ExitDeps{
		Barrier: g.Barrier, Light: g.Light, Terminal: edc,
		Store: h.store, Payments: h.pay, Tariffs: fakeTariffs{base: 5000},
		Auditor: h.audit,
		Site:    SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000, LostTicketPenalty: 20000},
		NodeID:  "n1", TenantID: "t1", SiteID: "s1", GateLabel: "GATE-OUT-01",
		Online: func() bool { return h.online },
		Now:    func() time.Time { return h.nowT },
	}
	d.ArmTimeout = func(_ time.Duration, r string) { h.armed = append(h.armed, r) }
	d.CancelTimer = func() { h.armed = nil }
	h.c = NewExitController(d)
	return h
}

func foundTx() ExitTx {
	return ExitTx{
		ID: "tx-1", VehicleType: "mobil", Found: true, ImgInKey: "img/in/1.jpg",
		EntryTime: time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
	}
}

func (h *xharness) fire(t *testing.T, e XEvent) {
	t.Helper()
	if err := h.c.Handle(context.Background(), e); err != nil {
		t.Fatalf("Handle(kind=%d): %v", e.Kind, err)
	}
}

func (h *xharness) barrier() hw.BarrierState {
	st, _ := h.g.Barrier.State(context.Background())
	return st
}

// ── tests ──

func TestExitCashHappyPath(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	if h.c.State() != XIdentifying {
		t.Fatalf("LD3: state=%s", h.c.State())
	}
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	if h.c.State() != XTransactionFound {
		t.Fatalf("identify: state=%s", h.c.State())
	}
	// 2 jam × 5000 = 10000.
	if h.c.Amount() != 10000 {
		t.Fatalf("tarif salah: %d", h.c.Amount())
	}
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	if h.c.State() != XFareCalculated {
		t.Fatalf("photo match: state=%s", h.c.State())
	}
	// Kasir bayar tunai, uang cukup + kembalian.
	h.fire(t, XEvent{Kind: XEvPaymentSelect, Method: MethodCash, Tendered: 20000})
	if h.c.State() != XOpen || h.barrier() != hw.BarrierOpen {
		t.Fatalf("setelah bayar: state=%s barrier=%v", h.c.State(), h.barrier())
	}
	if len(h.pay.settled) != 1 || len(h.store.completed) != 1 || h.store.completed[0] != 10000 {
		t.Fatalf("pembayaran/complete tidak benar: settled=%v completed=%v", h.pay.settled, h.store.completed)
	}
	// Kendaraan maju (lepas loop keluar depan), lewat, lalu tutup.
	h.fire(t, XEvent{Kind: XEvLD3, High: false})
	h.fire(t, XEvent{Kind: XEvLD4, High: true})
	h.fire(t, XEvent{Kind: XEvLD4, High: false})
	if h.c.State() != XIdle || h.barrier() != hw.BarrierClosed {
		t.Fatalf("penutupan: state=%s barrier=%v", h.c.State(), h.barrier())
	}
	if !h.audit.has("VEHICLE_EXITED") {
		t.Fatal("harus ada audit VEHICLE_EXITED")
	}
}

func TestExitMemberFree(t *testing.T) {
	tx := foundTx()
	tx.MembershipActive = true
	h := newXHarness(t, tx)
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyRFID, UID: "04AABB"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	if h.c.State() != XOpen {
		t.Fatalf("member: harus langsung buka, state=%s", h.c.State())
	}
	if h.c.Amount() != 0 || len(h.store.completed) != 1 || h.store.completed[0] != 0 {
		t.Fatalf("member harus gratis: amount=%d completed=%v", h.c.Amount(), h.store.completed)
	}
}

func TestExitPhotoMismatchDisputeThenOverride(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: false})
	if h.c.State() != XDisputeHold {
		t.Fatalf("foto tak cocok: harus DISPUTE_HOLD, state=%s", h.c.State())
	}
	if len(h.store.disputes) != 1 || !h.audit.has("PHOTO_MISMATCH") {
		t.Fatal("harus MarkDispute + audit PHOTO_MISMATCH")
	}
	h.fire(t, XEvent{Kind: XEvOverride})
	if h.c.State() != XFareCalculated || !h.audit.has("DISPUTE_OVERRIDE") {
		t.Fatalf("override: state=%s", h.c.State())
	}
}

func TestExitEDCApproved(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.edc.Approve = true
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	h.fire(t, XEvent{Kind: XEvPaymentSelect, Method: MethodEDCEmoney})
	if h.c.State() != XOpen {
		t.Fatalf("EDC approved: harus OPEN, state=%s", h.c.State())
	}
	if len(h.pay.settled) != 1 {
		t.Fatalf("EDC harus settle, settled=%v", h.pay.settled)
	}
}

// D8: EDC timeout → tetap PENDING (tidak fail, tidak double-charge), audit reconcile.
func TestExitEDCTimeoutLeavesPendingForReconcile(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.edc.Approve = true
	h.edc.Delay = 50 * time.Millisecond
	h.edc.SettleOnTimeout = true
	// Context berdurasi pendek agar Charge timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	// Panggil langsung dengan ctx timeout untuk fase pembayaran.
	if err := h.c.Handle(ctx, XEvent{Kind: XEvPaymentSelect, Method: MethodEDCEmoney}); err != nil {
		t.Fatalf("payment handle: %v", err)
	}
	if h.c.State() != XAwaitingPayment {
		t.Fatalf("timeout EDC: harus tetap AWAITING_PAYMENT, state=%s", h.c.State())
	}
	if len(h.pay.settled) != 0 || len(h.pay.failed) != 0 {
		t.Fatalf("EDC timeout tidak boleh settle/ fail (PENDING): settled=%v failed=%v", h.pay.settled, h.pay.failed)
	}
	if !h.audit.has("EDC_TIMEOUT_RECONCILE") {
		t.Fatal("harus ada audit EDC_TIMEOUT_RECONCILE")
	}
}

func TestExitQRISDisabledOffline(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.online = false
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	h.fire(t, XEvent{Kind: XEvPaymentSelect, Method: MethodQRIS})
	// QRIS ditolak saat offline → tidak ada Begin, tetap menunggu.
	if len(h.pay.begun) != 0 {
		t.Fatalf("QRIS offline tidak boleh memulai pembayaran, begun=%v", h.pay.begun)
	}
	if h.c.State() != XAwaitingPayment {
		t.Fatalf("harus tetap AWAITING_PAYMENT, state=%s", h.c.State())
	}
}

func TestExitCashInsufficientStays(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	h.fire(t, XEvent{Kind: XEvPaymentSelect, Method: MethodCash, Tendered: 5000}) // kurang dari 10000
	if h.c.State() != XAwaitingPayment {
		t.Fatalf("uang kurang: harus tetap AWAITING_PAYMENT, state=%s", h.c.State())
	}
	if len(h.pay.settled) != 0 {
		t.Fatal("tidak boleh settle saat uang kurang")
	}
	// Tender ulang cukup → berhasil.
	h.fire(t, XEvent{Kind: XEvPaymentSelect, Method: MethodCash, Tendered: 10000})
	if h.c.State() != XOpen {
		t.Fatalf("tender ulang: harus OPEN, state=%s", h.c.State())
	}
}

func TestExitPaymentDeclineThreeTimes(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.edc.Approve = false // selalu ditolak
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyTicket, Ticket: "TKT-A"})
	h.fire(t, XEvent{Kind: XEvPhotoVerdict, Match: true})
	for i := 0; i < 3; i++ {
		h.fire(t, XEvent{Kind: XEvPaymentSelect, Method: MethodEDCEmoney})
	}
	// 3× gagal → kembali ke FARE_CALCULATED (§5.5.1).
	if h.c.State() != XFareCalculated {
		t.Fatalf("3x gagal: harus FARE_CALCULATED, state=%s", h.c.State())
	}
	if len(h.pay.failed) != 3 {
		t.Fatalf("harus 3 kegagalan tercatat, got %d", len(h.pay.failed))
	}
}

func TestExitLostTicket(t *testing.T) {
	h := newXHarness(t, foundTx())
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvLostTicket, Plate: "B1234XYZ"})
	// max harian 30000 + denda 20000 = 50000.
	if h.c.State() != XFareCalculated || h.c.Amount() != 50000 {
		t.Fatalf("tiket hilang: state=%s amount=%d", h.c.State(), h.c.Amount())
	}
	if !h.audit.has("LOST_TICKET") {
		t.Fatal("harus ada audit LOST_TICKET")
	}
}

func TestExitUnregisteredVehicle(t *testing.T) {
	h := newXHarness(t, ExitTx{Found: false})
	h.fire(t, XEvent{Kind: XEvLD3, High: true})
	h.fire(t, XEvent{Kind: XEvIdentifyPlate, Plate: "B9999ZZ"})
	if h.c.State() != XFareCalculated || h.c.Amount() != 30000 {
		t.Fatalf("tak terdaftar: state=%s amount=%d (harus max harian)", h.c.State(), h.c.Amount())
	}
	if !h.audit.has("UNREGISTERED_EXIT") {
		t.Fatal("harus ada audit UNREGISTERED_EXIT critical")
	}
}
