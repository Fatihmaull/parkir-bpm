package memstore

import (
	"context"
	"testing"
	"time"
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
