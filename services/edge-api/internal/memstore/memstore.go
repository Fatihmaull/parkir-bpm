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
	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
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
	id            string
	uid           string
	plates        []string
	vehicleType   string
	validUntil    time.Time
	presence      string // IN|OUT
	presenceSince time.Time
	blocked       bool
	expired       bool
}

type payment struct {
	id      string
	txID    string
	method  string
	amount  int64
	status  string // PENDING|SETTLED|FAILED
	shiftID string // "" bila tak ada shift terbuka saat payment dibuat (§6.4)
}

type shift struct {
	id           string
	operatorID   string
	openedAt     time.Time
	closedAt     time.Time // zero = masih OPEN
	openingFloat int64
	declaredCash int64
	hasDeclared  bool
	systemCash   int64
	systemEDC    int64
	systemQRIS   int64
	variance     int64
	note         string
	status       string // OPEN|CLOSED|VARIANCE
}

// Store menyimpan seluruh state operasional in-memory.
type Store struct {
	mu             sync.Mutex
	now            func() time.Time
	vehicles       map[string]*vehicle
	members        map[string]*member // key: rfid uid
	payments       map[string]*payment
	rates          map[string]gate.RateCard // key: vehicle type
	chain          *audit.Chain
	auditLog       []audit.Entry
	ticketN        int
	outbox         *outbox.Mem
	ocrLogs        []OcrLog
	shifts         map[string]*shift
	currentShiftID string // "" = tak ada shift terbuka. Satu terminal in-memory = satu shift aktif.
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
		outbox:   outbox.NewMem(),
		shifts:   make(map[string]*shift),
	}
}

// Outbox mengembalikan antrean sinkronisasi (untuk sync agent & /health). Dikembalikan sebagai
// interface outbox.Store (bukan *outbox.Mem konkret) supaya memstore.Store dan pgstore.Store
// (task 5.1) punya tanda tangan metode yang identik — syarat keduanya memenuhi gatesvc.Store.
func (s *Store) Outbox() outbox.Store { return s.outbox }

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
	// Transactional outbox (§10.1): antrean sync ditulis "bersama" perubahan data.
	s.outbox.Enqueue("vehicles_log", v.id, map[string]any{
		"id": v.id, "status": v.status, "ticket_code": v.ticketCode,
		"membership_id": v.membershipID, "entry_time": v.entryTime,
	})
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
	s.outbox.Enqueue("vehicles_log", v.id, map[string]any{
		"id": v.id, "status": v.status, "amount": v.amount, "flags": v.flags,
	})
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
	s.payments[id] = &payment{
		id: id, txID: txID, method: method, amount: amount, status: "PENDING",
		shiftID: s.currentShiftID, // §6.4: payment tercatat ke shift yang sedang terbuka, kalau ada
	}
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
	m.presenceSince = s.now()
	return gate.MemberDecision{Allowed: true, MembershipID: m.id}, nil
}

// ── View untuk dashboard (§12) ──

type TxnView struct {
	ID          string    `json:"id"`
	Ticket      string    `json:"ticket"`
	VehicleType string    `json:"vehicle_type"`
	PlateIn     string    `json:"plate_in"`
	EntryTime   time.Time `json:"entry_time"`
	Amount      int64     `json:"amount"`
	Status      string    `json:"status"`
	Flags       []string  `json:"flags"`
}

type PaymentView struct {
	Method string `json:"method"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
	TxID   string `json:"tx_id"`
}

type MemberView struct {
	ID          string   `json:"id"`
	RfidUID     string   `json:"rfid_uid"`
	Plates      []string `json:"plates"`
	VehicleType string   `json:"vehicle_type"`
	ValidUntil  string   `json:"valid_until"`
	Presence    string   `json:"presence"`
	Status      string   `json:"status"`
}

// ShiftView — ringkasan satu shift kasir untuk dashboard (§6.4/§12.6).
type ShiftView struct {
	ID           string `json:"id"`
	OperatorID   string `json:"operator_id"`
	OpenedAt     string `json:"opened_at"`
	ClosedAt     string `json:"closed_at,omitempty"`
	OpeningFloat int64  `json:"opening_float"`
	DeclaredCash *int64 `json:"declared_cash,omitempty"`
	SystemCash   int64  `json:"system_cash"`
	SystemEDC    int64  `json:"system_edc"`
	SystemQRIS   int64  `json:"system_qris"`
	Variance     *int64 `json:"variance,omitempty"`
	Note         string `json:"note,omitempty"`
	Status       string `json:"status"` // OPEN|CLOSED|VARIANCE
}

// Transactions mengembalikan seluruh vehicles_log (untuk Catatan Keuangan & Volume §12.2/12.3).
func (s *Store) TransactionViews() []TxnView {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TxnView
	for _, v := range s.vehicles {
		if v.status == "DRAFT" {
			continue
		}
		out = append(out, TxnView{
			ID: v.id, Ticket: v.ticketCode, VehicleType: v.vehicleType, PlateIn: v.plateIn,
			EntryTime: v.entryTime, Amount: v.amount, Status: v.status, Flags: v.flags,
		})
	}
	return out
}

func (s *Store) PaymentViews() []PaymentView {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PaymentView
	for _, p := range s.payments {
		out = append(out, PaymentView{Method: p.method, Amount: p.amount, Status: p.status, TxID: p.txID})
	}
	return out
}

func (s *Store) MemberViews() []MemberView {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MemberView
	for _, m := range s.members {
		status := "active"
		if m.blocked {
			status = "blocked"
		} else if m.expired {
			status = "expired"
		}
		out = append(out, MemberView{
			ID: m.id, RfidUID: m.uid, Plates: m.plates, VehicleType: m.vehicleType,
			ValidUntil: m.validUntil.Format("2006-01-02"), Presence: m.presence, Status: status,
		})
	}
	return out
}

// ── Rekonsiliasi shift (§6.4/§12.6, task 7.4) ──

// OpenShift membuka shift baru. Satu terminal in-memory = satu shift aktif — membuka shift
// kedua sebelum yang pertama ditutup adalah kesalahan operator, bukan kondisi yang ditolerir
// diam-diam (kalau dibolehkan, payment berikutnya akan taggingnya ambigu: shift mana?).
func (s *Store) OpenShift(ctx context.Context, operatorID string, openingFloat int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentShiftID != "" {
		return "", fmt.Errorf("memstore: shift %s masih terbuka, tutup dulu sebelum buka baru", s.currentShiftID)
	}
	id := ids.NewV7()
	s.shifts[id] = &shift{
		id: id, operatorID: operatorID, openedAt: s.now(),
		openingFloat: openingFloat, status: "OPEN",
	}
	s.currentShiftID = id
	return id, nil
}

// CloseShift menghitung total sistem per metode dari payment SETTLED yang tertaut shift ini,
// lalu selisih = dilaporkan - (kas awal + tunai sistem) — rumus §6.4 persis. Selisih ≠ 0
// WAJIB disertai note (alasan) — tak ada cara menutup shift senjang tanpa keterangan.
func (s *Store) CloseShift(ctx context.Context, shiftID string, declaredCash int64, note string) (ShiftView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.shifts[shiftID]
	if !ok {
		return ShiftView{}, fmt.Errorf("memstore: shift %s tidak ditemukan", shiftID)
	}
	if sh.status != "OPEN" {
		return ShiftView{}, fmt.Errorf("memstore: shift %s sudah ditutup", shiftID)
	}

	var systemCash, systemEDC, systemQRIS int64
	for _, p := range s.payments {
		if p.shiftID != shiftID || p.status != "SETTLED" {
			continue
		}
		switch p.method {
		case gate.MethodCash:
			systemCash += p.amount
		case gate.MethodEDCEmoney, gate.MethodEDCDebit:
			systemEDC += p.amount
		case gate.MethodQRIS, gate.MethodEwallet:
			systemQRIS += p.amount
		}
	}
	variance := declaredCash - (sh.openingFloat + systemCash)
	if variance != 0 && note == "" {
		return ShiftView{}, fmt.Errorf("memstore: selisih %d — wajib isi catatan alasan (§6.4)", variance)
	}

	sh.closedAt = s.now()
	sh.declaredCash, sh.hasDeclared = declaredCash, true
	sh.systemCash, sh.systemEDC, sh.systemQRIS = systemCash, systemEDC, systemQRIS
	sh.variance = variance
	sh.note = note
	if variance != 0 {
		sh.status = "VARIANCE"
	} else {
		sh.status = "CLOSED"
	}
	if s.currentShiftID == shiftID {
		s.currentShiftID = ""
	}
	return shiftViewOf(sh), nil
}

// ShiftViews mengembalikan seluruh shift (untuk GET /api/v1/shifts).
func (s *Store) ShiftViews() []ShiftView {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ShiftView
	for _, sh := range s.shifts {
		out = append(out, shiftViewOf(sh))
	}
	return out
}

func shiftViewOf(sh *shift) ShiftView {
	v := ShiftView{
		ID: sh.id, OperatorID: sh.operatorID, OpenedAt: sh.openedAt.Format(time.RFC3339),
		OpeningFloat: sh.openingFloat, SystemCash: sh.systemCash, SystemEDC: sh.systemEDC,
		SystemQRIS: sh.systemQRIS, Note: sh.note, Status: sh.status,
	}
	if !sh.closedAt.IsZero() {
		v.ClosedAt = sh.closedAt.Format(time.RFC3339)
	}
	if sh.hasDeclared {
		v.DeclaredCash = &sh.declaredCash
		v.Variance = &sh.variance
	}
	return v
}

// OccupancyByType menghitung kendaraan IN_PREMISES per jenis (Mapping Slot §12.4).
func (s *Store) OccupancyByType() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, v := range s.vehicles {
		if v.status == "IN_PREMISES" {
			out[v.vehicleType]++
		}
	}
	return out
}

// ── CRON job logic (§8.3) — idempoten ──

// ExpireMemberships menandai member yang valid_until < now sebagai kedaluwarsa. Kembalikan jumlah.
func (s *Store) ExpireMemberships(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.members {
		if !m.expired && !now.Before(m.validUntil) {
			m.expired = true
			n++
		}
	}
	return n
}

// ResetStalePresence mereset member yang berstatus IN lebih dari `hours` jam → OUT (§8.2).
func (s *Store) ResetStalePresence(now time.Time, hours int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	for _, m := range s.members {
		if m.presence == "IN" && !m.presenceSince.IsZero() && m.presenceSince.Before(cutoff) {
			m.presence = "OUT"
			n++
		}
	}
	return n
}

// VerifyChain memverifikasi rantai audit penuh (§9.4). Kembalikan (brokenSeq, ok).
func (s *Store) VerifyChain() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return audit.Verify(s.auditLog)
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

// OcrLog — satu baris log OCR (§7.2). Ditulis SELALU, sukses maupun gagal.
type OcrLog struct {
	CapturedAt      time.Time `json:"captured_at"`
	GateID          string    `json:"gate_id"`
	RawText         string    `json:"raw_text"`
	NormalizedPlate string    `json:"normalized_plate"`
	Confidence      float64   `json:"confidence"`
	Verdict         string    `json:"verdict"`
	LatencyMs       int       `json:"latency_ms"`
	EngineVersion   string    `json:"engine_version"`
}

// WriteOCR menyimpan satu baris ocr_logs.
func (s *Store) WriteOCR(l OcrLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ocrLogs = append(s.ocrLogs, l)
}

// OCRLogs mengembalikan salinan ocr_logs (untuk analitik akurasi & UI).
func (s *Store) OCRLogs() []OcrLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]OcrLog(nil), s.ocrLogs...)
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
