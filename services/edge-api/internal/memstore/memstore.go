// Package memstore — implementasi in-memory dari seluruh interface yang dibutuhkan gate
// controller (Store, ExitStore, Payments, Members, Tariffs, Auditor, TicketGen).
//
// Tujuannya: menjalankan edge-api penuh tanpa PostgreSQL (filosofi feature-flag mock, D12) —
// untuk demo, pelatihan, dan test integrasi. Implementasi pgx nyata akan meniru kontrak yang sama.
// Audit log tetap memakai rantai hash nyata (audit.Chain) sehingga perilaku identik dengan produksi.
package memstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
)

type vehicle struct {
	id           string
	ticketCode   string
	plateIn      string
	vehicleType  string
	membershipID string
	imgInKey     string
	entryTime    time.Time
	status       string // DRAFT|IN_PREMISES|COMPLETED|VOID|DISPUTE
	amount       int64
	flags        []string
}

type member struct {
	id          string
	uid         string
	plates      []string
	vehicleType string
	validUntil  time.Time
	presence    string // IN|OUT
	blocked     bool
}

type payment struct {
	id     string
	txID   string
	method string
	amount int64
	status string // PENDING|SETTLED|FAILED
}

// Store menyimpan seluruh state operasional in-memory.
type Store struct {
	mu       sync.Mutex
	now      func() time.Time
	vehicles map[string]*vehicle
	members  map[string]*member // key: rfid uid
	payments map[string]*payment
	rates    map[string]gate.RateCard // key: vehicle type
	chain    *audit.Chain
	auditLog []audit.Entry
	ticketN  int
}

func New(nodeID string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{
		now:      now,
		vehicles: make(map[string]*vehicle),
		members:  make(map[string]*member),
		payments: make(map[string]*payment),
		rates:    make(map[string]gate.RateCard),
		chain:    audit.NewChain(nodeID, 0, ""),
	}
}

// ── seed helpers (untuk demo/test) ──

func (s *Store) SetRate(vehicleType string, rc gate.RateCard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rates[vehicleType] = rc
}

func (s *Store) AddMember(uid string, plates []string, vehicleType string, validUntil time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ids.NewV7()
	s.members[uid] = &member{
		id: id, uid: uid, plates: plates, vehicleType: vehicleType,
		validUntil: validUntil, presence: "OUT",
	}
	return id
}

// ── gate.Store (entry) ──

func (s *Store) CreateDraft(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ids.NewV7()
	s.vehicles[id] = &vehicle{id: id, status: "DRAFT", entryTime: s.now(), vehicleType: "mobil"}
	return id, nil
}

func (s *Store) CommitInPremises(ctx context.Context, txID, ticketCode, plate, membershipID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vehicles[txID]
	if !ok {
		return fmt.Errorf("memstore: tx %s tidak ditemukan", txID)
	}
	v.status = "IN_PREMISES"
	v.ticketCode = ticketCode
	v.plateIn = plate
	v.membershipID = membershipID
	if v.entryTime.IsZero() {
		v.entryTime = s.now()
	}
	return nil
}

func (s *Store) Void(ctx context.Context, txID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vehicles[txID]; ok {
		v.status = "VOID"
	}
	return nil
}

// ── gate.ExitStore ──

func (s *Store) Lookup(ctx context.Context, key gate.LookupKey) (gate.ExitTx, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *vehicle
	for _, v := range s.vehicles {
		if v.status != "IN_PREMISES" {
			continue
		}
		switch {
		case key.Ticket != "" && v.ticketCode == key.Ticket:
			found = v
		case key.Plate != "" && v.plateIn == key.Plate:
			found = v
		case key.UID != "":
			if m, ok := s.members[key.UID]; ok && v.membershipID == m.id {
				found = v
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		return gate.ExitTx{Found: false}, nil
	}
	memberActive := false
	if found.membershipID != "" {
		for _, m := range s.members {
			if m.id == found.membershipID && !m.blocked && s.now().Before(m.validUntil) {
				memberActive = true
			}
		}
	}
	return gate.ExitTx{
		ID: found.id, VehicleType: found.vehicleType, EntryTime: found.entryTime,
		MembershipActive: memberActive, ImgInKey: found.imgInKey, Found: true,
	}, nil
}

func (s *Store) Complete(ctx context.Context, txID string, amount int64, plateOut string, flags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vehicles[txID]
	if !ok {
		return fmt.Errorf("memstore: tx %s tidak ditemukan", txID)
	}
	v.status = "COMPLETED"
	v.amount = amount
	v.flags = flags
	// Anti-passback: kendaraan member keluar → presence OUT (§8.2).
	if v.membershipID != "" {
		for _, m := range s.members {
			if m.id == v.membershipID {
				m.presence = "OUT"
			}
		}
	}
	return nil
}

func (s *Store) MarkDispute(ctx context.Context, txID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vehicles[txID]; ok {
		v.status = "DISPUTE"
	}
	return nil
}

// ── gate.Payments ──

func (s *Store) Begin(ctx context.Context, txID, method string, amount int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ids.NewV7()
	s.payments[id] = &payment{id: id, txID: txID, method: method, amount: amount, status: "PENDING"}
	return id, nil
}

func (s *Store) Settle(ctx context.Context, payID string, info gate.SettleInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.payments[payID]; ok {
		p.status = "SETTLED"
	}
	return nil
}

func (s *Store) Fail(ctx context.Context, payID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.payments[payID]; ok {
		p.status = "FAILED"
	}
	return nil
}

// ── gate.Members (anti-passback §8.2) ──

func (s *Store) ValidateEntry(ctx context.Context, uid string) (gate.MemberDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.members[uid]
	if !ok {
		return gate.MemberDecision{Allowed: false, Reason: "NOT_MEMBER"}, nil
	}
	if m.blocked {
		return gate.MemberDecision{Allowed: false, Reason: "BLOCKED"}, nil
	}
	if !s.now().Before(m.validUntil) {
		return gate.MemberDecision{Allowed: false, Reason: "EXPIRED"}, nil
	}
	if m.presence == "IN" {
		return gate.MemberDecision{Allowed: false, Reason: "ANTIPASSBACK_VIOLATION"}, nil
	}
	m.presence = "IN" // tap masuk: OUT → IN
	return gate.MemberDecision{Allowed: true, MembershipID: m.id}, nil
}

// ── gate.Tariffs ──

func (s *Store) Resolve(vehicleType string) (gate.RateCard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, ok := s.rates[vehicleType]
	return rc, ok
}

// ── gate.Auditor (rantai hash nyata) ──

func (s *Store) Record(ctx context.Context, e audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.chain.Append(e)
	if err != nil {
		return err
	}
	s.auditLog = append(s.auditLog, entry)
	return nil
}

// ActiveVehicle — ringkasan kendaraan yang masih di dalam (IN_PREMISES).
type ActiveVehicle struct {
	ID          string    `json:"id"`
	Ticket      string    `json:"ticket"`
	VehicleType string    `json:"vehicle_type"`
	Plate       string    `json:"plate"`
	EntryTime   time.Time `json:"entry_time"`
	IsMember    bool      `json:"is_member"`
}

// ActiveVehicles mengembalikan kendaraan berstatus IN_PREMISES (untuk POS & metrik).
func (s *Store) ActiveVehicles() []ActiveVehicle {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ActiveVehicle
	for _, v := range s.vehicles {
		if v.status != "IN_PREMISES" {
			continue
		}
		out = append(out, ActiveVehicle{
			ID: v.id, Ticket: v.ticketCode, VehicleType: v.vehicleType,
			Plate: v.plateIn, EntryTime: v.entryTime, IsMember: v.membershipID != "",
		})
	}
	return out
}

// AuditEntries mengembalikan salinan rantai audit (untuk /health & test).
func (s *Store) AuditEntries() []audit.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.Entry(nil), s.auditLog...)
}

// ── gate.TicketGen ──

func (s *Store) Next() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ticketN++
	code := fmt.Sprintf("TKT-%06d", s.ticketN)
	return code, "PARKIR:" + code
}
