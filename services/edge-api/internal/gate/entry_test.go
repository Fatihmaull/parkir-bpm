package gate

import (
	"context"
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/sim"
)

// ── fakes ──

type fakeStore struct {
	drafts  int
	commits []string
	voids   []string
	lastTx  string
}

func (s *fakeStore) CreateDraft(ctx context.Context) (string, error) {
	s.drafts++
	s.lastTx = "tx-" + string(rune('0'+s.drafts))
	return s.lastTx, nil
}
func (s *fakeStore) CommitInPremises(ctx context.Context, tx, ticket, plate, mem string) error {
	s.commits = append(s.commits, tx)
	return nil
}
func (s *fakeStore) Void(ctx context.Context, tx, reason string) error {
	s.voids = append(s.voids, tx)
	return nil
}

type fakeMembers struct {
	allow  bool
	reason string
}

func (m fakeMembers) ValidateEntry(ctx context.Context, uid string) (MemberDecision, error) {
	return MemberDecision{Allowed: m.allow, MembershipID: "mem-1", Reason: m.reason}, nil
}

type fakeAudit struct{ events []audit.Event }

func (a *fakeAudit) Record(ctx context.Context, e audit.Event) error {
	a.events = append(a.events, e)
	return nil
}
func (a *fakeAudit) has(evType string) bool {
	for _, e := range a.events {
		if e.EventType == evType {
			return true
		}
	}
	return false
}

type seqTickets struct{ n int }

func (t *seqTickets) Next() (string, string) {
	t.n++
	code := "TKT-" + string(rune('A'+t.n-1))
	return code, "QR:" + code
}

// harness membangun controller + simulator gerbang. Timer diganti perekam manual —
// test memicu Event{Kind:EvTimeout} sendiri, sehingga transisi deterministik (tanpa sleep).
type harness struct {
	c     *Controller
	g     *sim.Gate
	store *fakeStore
	audit *fakeAudit
	armed []string // reason timeout yang sedang diarm
}

func newHarness(t *testing.T, allowMember bool, memReason string) *harness {
	t.Helper()
	g := sim.NewGate()
	h := &harness{g: g, store: &fakeStore{}, audit: &fakeAudit{}}
	d := Deps{
		Barrier: g.Barrier, Light: g.Light, Printer: g.Printer,
		Store: h.store, Members: fakeMembers{allow: allowMember, reason: memReason},
		Auditor: h.audit, Tickets: &seqTickets{},
		NodeID: "n1", TenantID: "t1", SiteID: "s1", GateLabel: "GATE-IN-01",
	}
	d.ArmTimeout = func(_ time.Duration, reason string) { h.armed = append(h.armed, reason) }
	d.CancelTimer = func() { h.armed = nil }
	h.c = NewController(d)
	return h
}

func (h *harness) fire(t *testing.T, e Event) {
	t.Helper()
	if err := h.c.Handle(context.Background(), e); err != nil {
		t.Fatalf("Handle(kind=%d): %v", e.Kind, err)
	}
}

func (h *harness) barrierState() hw.BarrierState {
	st, _ := h.g.Barrier.State(context.Background())
	return st
}

// ── tests ──

// Jalur kasual bahagia: LD1 → tombol → tiket → ambil → palang buka → lewat → tutup → IDLE.
func TestCasualHappyPath(t *testing.T) {
	h := newHarness(t, false, "")

	h.fire(t, Event{Kind: EvLD1, High: true})
	if h.c.State() != StateVehiclePresent || h.store.drafts != 1 {
		t.Fatalf("setelah LD1: state=%s drafts=%d", h.c.State(), h.store.drafts)
	}

	h.fire(t, Event{Kind: EvButton})
	if h.c.State() != StateAwaitingPull {
		t.Fatalf("setelah tombol: state=%s", h.c.State())
	}

	h.fire(t, Event{Kind: EvTicketTaken})
	if h.c.State() != StateOpen {
		t.Fatalf("setelah tiket diambil: state=%s", h.c.State())
	}
	if h.barrierState() != hw.BarrierOpen {
		t.Fatal("palang harus terbuka")
	}
	if len(h.store.commits) != 1 {
		t.Fatalf("harus commit IN_PREMISES sekali, got %d", len(h.store.commits))
	}
	if h.g.Light.Pattern() != hw.PatternGreen {
		t.Fatal("lampu harus hijau saat OPEN")
	}

	// Kendaraan bergerak maju: LD1 turun (lepas dari loop depan), lalu lewat LD2 naik & turun.
	h.fire(t, Event{Kind: EvLD1, High: false})
	h.fire(t, Event{Kind: EvLD2, High: true})
	if h.c.State() != StateClearing {
		t.Fatalf("LD2 naik: state=%s", h.c.State())
	}
	h.fire(t, Event{Kind: EvLD2, High: false})
	if h.c.State() != StateIdle {
		t.Fatalf("LD2 turun: harus IDLE, got %s", h.c.State())
	}
	if h.barrierState() != hw.BarrierClosed {
		t.Fatal("palang harus tertutup di IDLE")
	}
	if !h.audit.has("VEHICLE_ENTERED") {
		t.Fatal("harus ada audit VEHICLE_ENTERED")
	}
}

// Interlock (P4/§5.4.1): palang tak menutup selama LD2 HIGH.
func TestInterlockPreventsCloseWhileLD2High(t *testing.T) {
	h := newHarness(t, false, "")
	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	h.fire(t, Event{Kind: EvTicketTaken})      // OPEN, palang terbuka
	h.fire(t, Event{Kind: EvLD1, High: false}) // mobil tunggal, lepas loop depan
	h.fire(t, Event{Kind: EvLD2, High: true})  // masuk CLEARING, kendaraan di bawah palang

	// Simulasikan loop bawah fisik tetap HIGH di simulator → Close harus ditolak.
	h.g.LoopPost.Drive(true)
	// Kirim LD2 falling secara logis TAPI loop fisik masih HIGH (kondisi tak konsisten/mobil nyangkut):
	// controller mencoba menutup, Barrier menolak (interlock), tetap di CLEARING.
	h.fire(t, Event{Kind: EvLD2, High: false})
	if h.c.State() != StateClearing {
		t.Fatalf("interlock: harus tetap CLEARING saat loop bawah HIGH, got %s", h.c.State())
	}
	if h.barrierState() != hw.BarrierOpen {
		t.Fatal("palang harus tetap terbuka saat interlock aktif")
	}

	// Loop bawah akhirnya LOW → penutupan berhasil.
	h.g.LoopPost.Drive(false)
	h.fire(t, Event{Kind: EvLD2, High: false})
	if h.c.State() != StateIdle || h.barrierState() != hw.BarrierClosed {
		t.Fatalf("setelah loop LOW: harus IDLE & tertutup, got %s / %v", h.c.State(), h.barrierState())
	}
}

// Anti-tailgating (§5.4.1): saat menutup, bila LD1 masih HIGH → siklus baru untuk mobil kedua.
func TestAntiTailgatingStartsNewCycle(t *testing.T) {
	h := newHarness(t, false, "")
	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	h.fire(t, Event{Kind: EvTicketTaken})
	h.fire(t, Event{Kind: EvLD2, High: true})
	// Mobil pertama lewat (LD2 falling), TAPI mobil kedua sudah menempel (LD1 tetap HIGH).
	h.fire(t, Event{Kind: EvLD2, High: false})
	if h.c.State() != StateVehiclePresent {
		t.Fatalf("anti-tailgating: mobil kedua harus mulai siklus baru, got %s", h.c.State())
	}
	if h.store.drafts != 2 {
		t.Fatalf("harus ada draft kedua untuk mobil kedua, got %d", h.store.drafts)
	}
	if !h.audit.has("ANTITAILGATE_NEW_CYCLE") {
		t.Fatal("harus ada audit ANTITAILGATE_NEW_CYCLE")
	}
}

// Tiket tidak diambil → timeout → retract + VOID (§5.4.2).
func TestTicketPullTimeoutVoids(t *testing.T) {
	h := newHarness(t, false, "")
	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	if len(h.armed) == 0 || h.armed[len(h.armed)-1] != toPull {
		t.Fatalf("timeout ticket_pull harus diarm, armed=%v", h.armed)
	}
	h.fire(t, Event{Kind: EvTimeout, Reason: toPull})
	if h.c.State() != StateIdle {
		t.Fatalf("setelah timeout: harus IDLE, got %s", h.c.State())
	}
	if len(h.store.voids) != 1 {
		t.Fatalf("transaksi harus di-VOID, voids=%d", len(h.store.voids))
	}
	if !h.audit.has("TICKET_VOID") {
		t.Fatal("harus ada audit TICKET_VOID")
	}
}

// Member (RFID) ditolak anti-passback → REJECTED + audit warning.
func TestMemberRejectedAntipassback(t *testing.T) {
	h := newHarness(t, false, "ANTIPASSBACK_VIOLATION")
	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvRFID, UID: "04AABBCC"})
	if h.c.State() != StateRejected {
		t.Fatalf("tap ditolak: harus REJECTED, got %s", h.c.State())
	}
	if !h.audit.has("ANTIPASSBACK_VIOLATION") {
		t.Fatal("harus ada audit ANTIPASSBACK_VIOLATION")
	}
	if h.barrierState() != hw.BarrierClosed {
		t.Fatal("palang harus tetap tertutup untuk tap yang ditolak")
	}
	// Setelah display timeout → kembali IDLE.
	h.fire(t, Event{Kind: EvTimeout, Reason: toReject})
	if h.c.State() != StateIdle {
		t.Fatalf("setelah reject display: harus IDLE, got %s", h.c.State())
	}
}

// Member diterima → palang buka tanpa tiket, commit dengan membership_id.
func TestMemberAcceptedOpens(t *testing.T) {
	h := newHarness(t, true, "")
	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvRFID, UID: "04AABBCC"})
	if h.c.State() != StateOpen || h.barrierState() != hw.BarrierOpen {
		t.Fatalf("member diterima: harus OPEN & palang terbuka, got %s / %v", h.c.State(), h.barrierState())
	}
	if len(h.store.commits) != 1 {
		t.Fatal("harus commit IN_PREMISES untuk member")
	}
}

// Kertas habis → casual berhenti (LOCKED_NO_PAPER), audit critical (§5.4.3).
func TestPaperOutLocksCasual(t *testing.T) {
	h := newHarness(t, false, "")
	h.g.Printer.SetStatus(hw.PrinterPaperOut)
	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	if h.c.State() != StateLockedNoPaper {
		t.Fatalf("kertas habis: harus LOCKED_NO_PAPER, got %s", h.c.State())
	}
	if !h.audit.has("LOCKED_NO_PAPER") {
		t.Fatal("harus ada audit LOCKED_NO_PAPER")
	}
}

// Palang ditembus: LD2 naik saat IDLE → UNAUTHORIZED_PASSAGE critical (§5.4.1).
func TestUnauthorizedPassage(t *testing.T) {
	h := newHarness(t, false, "")
	h.fire(t, Event{Kind: EvLD2, High: true}) // tak ada otorisasi
	if !h.audit.has("UNAUTHORIZED_PASSAGE") {
		t.Fatal("harus ada audit UNAUTHORIZED_PASSAGE")
	}
}

// D3: kertas habis menghentikan casual, TAPI MEMBER TETAP MASUK (§5.4.3).
//
// Regresi. Keadaan LOCKED_NO_PAPER dulu tak muncul sama sekali di switch state, sehingga
// gerbang berhenti menerima event apa pun begitu masuk ke sana — termasuk tap member yang
// justru dijanjikan komentar di issueTicket. Ditemukan chaos test 3.6.
func TestPaperOutMemberTetapMasuk(t *testing.T) {
	h := newHarness(t, true, "")
	h.g.Printer.SetStatus(hw.PrinterPaperOut)

	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	if h.c.State() != StateLockedNoPaper {
		t.Fatalf("prasyarat: harus LOCKED_NO_PAPER, got %s", h.c.State())
	}

	h.fire(t, Event{Kind: EvRFID, UID: "04A1B2C3"})
	if h.c.State() != StateOpen || h.barrierState() != hw.BarrierOpen {
		t.Fatalf("member saat kertas habis: harus OPEN & palang terbuka, got %s / %v",
			h.c.State(), h.barrierState())
	}
	if len(h.store.commits) != 1 {
		t.Fatal("member harus tetap tercatat IN_PREMISES walau tanpa tiket")
	}
}

// Kertas diisi ulang → gerbang pulih tanpa perlu restart. Pengemudi menekan tombol lagi.
func TestPaperOutPulihSetelahKertasDiisi(t *testing.T) {
	h := newHarness(t, false, "")
	h.g.Printer.SetStatus(hw.PrinterPaperOut)

	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	if h.c.State() != StateLockedNoPaper {
		t.Fatalf("prasyarat: harus LOCKED_NO_PAPER, got %s", h.c.State())
	}

	h.g.Printer.SetStatus(hw.PrinterOK) // petugas mengisi kertas
	h.fire(t, Event{Kind: EvButton})

	if h.c.State() != StateAwaitingPull {
		t.Fatalf("setelah kertas diisi & tombol ditekan lagi: harus AWAITING_PULL, got %s",
			h.c.State())
	}
}

// Kendaraan yang menyerah dan pergi mengembalikan gerbang ke IDLE.
//
// Tanpa ini gerbang tetap terkunci bagi kendaraan BERIKUTNYA — yang tak punya urusan apa
// pun dengan kertas habis.
func TestPaperOutKendaraanPergiMembukaKunci(t *testing.T) {
	h := newHarness(t, false, "")
	h.g.Printer.SetStatus(hw.PrinterPaperOut)

	h.fire(t, Event{Kind: EvLD1, High: true})
	h.fire(t, Event{Kind: EvButton})
	if h.c.State() != StateLockedNoPaper {
		t.Fatalf("prasyarat: harus LOCKED_NO_PAPER, got %s", h.c.State())
	}

	h.fire(t, Event{Kind: EvLD1, High: false}) // kendaraan pergi

	if h.c.State() != StateIdle {
		t.Fatalf("kendaraan pergi: harus kembali IDLE, got %s", h.c.State())
	}
	if !h.audit.has("TICKET_VOID") {
		t.Fatal("draft yang ditinggalkan harus di-VOID, bukan menggantung")
	}
}
