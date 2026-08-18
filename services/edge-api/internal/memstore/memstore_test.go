package memstore

import (
	"context"
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
)

func TestExpireMemberships(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	s := New("n1", func() time.Time { return now })
	s.AddMember("A", nil, "mobil", now.Add(24*time.Hour))  // masih berlaku
	s.AddMember("B", nil, "mobil", now.Add(-1*time.Hour))  // sudah lewat
	if n := s.ExpireMemberships(now); n != 1 {
		t.Fatalf("harus 1 kedaluwarsa, got %d", n)
	}
	// Idempoten: run kedua tidak menambah.
	if n := s.ExpireMemberships(now); n != 0 {
		t.Fatalf("run kedua harus 0, got %d", n)
	}
}

func TestResetStalePresence(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cur := now.Add(-20 * time.Hour) // tap masuk 20 jam lalu
	s := New("n1", func() time.Time { return cur })
	s.AddMember("A", nil, "mobil", now.Add(24*time.Hour))
	// Member tap masuk (presence IN, presenceSince=cur).
	if _, err := s.ValidateEntry(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	// Sekarang jam maju; reset ambang 18 jam.
	s.now = func() time.Time { return now }
	if n := s.ResetStalePresence(now, 18); n != 1 {
		t.Fatalf("presence >18 jam harus direset, got %d", n)
	}
	// Sudah OUT → run kedua 0.
	if n := s.ResetStalePresence(now, 18); n != 0 {
		t.Fatalf("run kedua harus 0, got %d", n)
	}
}

// ── Rekonsiliasi shift (§6.4, task 7.4) ──

func TestShiftOpenCloseNoVariance(t *testing.T) {
	ctx := context.Background()
	s := New("n1", nil)
	shiftID, err := s.OpenShift(ctx, "op-1", 100_000)
	if err != nil {
		t.Fatalf("OpenShift: %v", err)
	}

	payID, err := s.Begin(ctx, "tx-1", gate.MethodCash, 50_000)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := s.Settle(ctx, payID, gate.SettleInfo{}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	report, err := s.CloseShift(ctx, shiftID, 150_000, "")
	if err != nil {
		t.Fatalf("CloseShift: %v", err)
	}
	if report.Status != "CLOSED" || report.SystemCash != 50_000 {
		t.Fatalf("harus CLOSED dengan system_cash 50000, dapat %+v", report)
	}
	if report.Variance == nil || *report.Variance != 0 {
		t.Fatalf("variance harus 0, dapat %+v", report.Variance)
	}
}

func TestShiftDoubleOpenRejected(t *testing.T) {
	ctx := context.Background()
	s := New("n1", nil)
	if _, err := s.OpenShift(ctx, "op-1", 0); err != nil {
		t.Fatalf("OpenShift pertama: %v", err)
	}
	if _, err := s.OpenShift(ctx, "op-1", 0); err == nil {
		t.Fatal("OpenShift kedua selagi satu masih terbuka harus ditolak")
	}
}

func TestShiftCloseRequiresNoteOnVariance(t *testing.T) {
	ctx := context.Background()
	s := New("n1", nil)
	shiftID, _ := s.OpenShift(ctx, "op-1", 0)

	if _, err := s.CloseShift(ctx, shiftID, 10_000, ""); err == nil {
		t.Fatal("selisih != 0 tanpa note harus ditolak")
	}
	report, err := s.CloseShift(ctx, shiftID, 10_000, "kelebihan tak diketahui")
	if err != nil {
		t.Fatalf("CloseShift dengan note: %v", err)
	}
	if report.Status != "VARIANCE" || report.Variance == nil || *report.Variance != 10_000 {
		t.Fatalf("harus VARIANCE dengan selisih 10000, dapat %+v", report)
	}

	// Shift yang sudah tertutup tak bisa ditutup lagi, dan payment SETELAH tertutup (shift
	// baru dibuka) tak ikut terhitung ke shift lama.
	if _, err := s.CloseShift(ctx, shiftID, 0, ""); err == nil {
		t.Fatal("menutup shift yang sudah tertutup harus gagal")
	}
}
