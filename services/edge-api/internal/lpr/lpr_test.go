package lpr

import (
	"context"
	"testing"
	"time"
)

func TestDegradedAlwaysUnread(t *testing.T) {
	r, _ := Degraded{}.Recognize(context.Background(), nil, "GATE-IN-01")
	if r.Verdict != VerdictUnread {
		t.Fatalf("LPR degradasi harus UNREAD (P2), got %s", r.Verdict)
	}
}

func TestDecideVerdictThresholds(t *testing.T) {
	if DecideVerdict(0.95, true, false) != VerdictAutoAccept {
		t.Fatal("0.95 valid → AUTO_ACCEPT")
	}
	if DecideVerdict(0.70, true, false) != VerdictNeedsReview {
		t.Fatal("0.70 → NEEDS_REVIEW")
	}
	if DecideVerdict(0.50, true, false) != VerdictUnread {
		t.Fatal("<0.60 → UNREAD")
	}
	if DecideVerdict(0.95, false, false) != VerdictUnread {
		t.Fatal("pola tak valid → UNREAD")
	}
	if DecideVerdict(0.95, true, true) != VerdictUnread {
		t.Fatal("timeout → UNREAD")
	}
}

func TestStubReturnsPlate(t *testing.T) {
	r, _ := Stub{Plate: "D1234AB", Confidence: 0.92, EngineVersion: "stub"}.Recognize(context.Background(), nil, "g")
	if r.NormalizedPlate != "D1234AB" || r.Verdict != VerdictAutoAccept {
		t.Fatalf("stub: %+v", r)
	}
}

func TestStubTimeoutUnread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	r, _ := Stub{Plate: "D1", Confidence: 0.9, Delay: 100 * time.Millisecond}.Recognize(ctx, nil, "g")
	if r.Verdict != VerdictUnread {
		t.Fatalf("timeout harus UNREAD, got %s", r.Verdict)
	}
}
