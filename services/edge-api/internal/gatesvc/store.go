package gatesvc

import (
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// Store — kontrak penyimpanan penuh yang dibutuhkan Service: state machine gerbang (lewat
// interface gate.* yang lebih kecil), plus pelaporan/dashboard/cron yang dipakai langsung oleh
// gatesvc & handler HTTP (cmd/edge-api).
//
// memstore.Store (D12) dan internal/pgstore (task 5.1) sama-sama memenuhi kontrak ini —
// itulah "interface identik" yang dituntut task 5.1: mengganti implementasi, bukan kontraknya.
// Tipe DTO (OcrLog, ActiveVehicle, TxnView, ...) sengaja tetap dari paket memstore, bukan
// diduplikasi — memstore adalah definisi kanonis bentuknya (JSON tag dsb), pgstore hanya
// mengimpornya untuk mengisi nilai dari baris Postgres.
type Store interface {
	gate.Store
	gate.ExitStore
	gate.Members
	gate.Auditor
	gate.TicketGen
	gate.Tariffs
	gate.Payments

	// ── pelaporan/dashboard (§12) ──
	// AddMember — pendaftaran/impor member dari dashboard (§12.13), bukan sekadar seed demo.
	AddMember(uid string, plates []string, vehicleType string, validUntil time.Time) (membershipID string)
	WriteOCR(memstore.OcrLog)
	OCRLogs() []memstore.OcrLog
	ActiveVehicles() []memstore.ActiveVehicle
	AuditEntries() []audit.Entry
	TransactionViews() []memstore.TxnView
	PaymentViews() []memstore.PaymentView
	MemberViews() []memstore.MemberView
	OccupancyByType() map[string]int

	// ── sync & audit ──
	Outbox() outbox.Store
	VerifyChain() (brokenSeq int64, ok bool)

	// ── CRON job logic (§8.3) ──
	ExpireMemberships(now time.Time) int
	ResetStalePresence(now time.Time, hours int) int
}

var _ Store = (*memstore.Store)(nil)
