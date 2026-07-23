package fare

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestGracePeriodFree(t *testing.T) {
	r := Calculate(Input{
		EntryTime: at("2026-07-24 10:00"), ExitTime: at("2026-07-24 10:10"),
		GraceMinutes: 15, BaseRate: 5000,
	})
	if !r.Free || r.Amount != 0 || r.Reason != "grace_period" {
		t.Fatalf("dalam grace harus gratis: %+v", r)
	}
}

func TestMemberFree(t *testing.T) {
	r := Calculate(Input{
		EntryTime: at("2026-07-24 08:00"), ExitTime: at("2026-07-24 20:00"),
		GraceMinutes: 15, MemberCoversSite: true, BaseRate: 5000,
	})
	if !r.Free || r.Amount != 0 || r.Reason != "member" {
		t.Fatalf("member harus gratis: %+v", r)
	}
}

func TestBasicHourlyCeil(t *testing.T) {
	// 1 jam 1 menit → dibulatkan ke 2 jam. base 5000 → 10000.
	r := Calculate(Input{
		EntryTime: at("2026-07-24 10:00"), ExitTime: at("2026-07-24 11:01"),
		GraceMinutes: 15, BaseRate: 5000,
	})
	if r.ChargedHours != 2 || r.Amount != 10000 {
		t.Fatalf("want 2 jam / 10000, got %+v", r)
	}
}

func TestFirstHourRateOverride(t *testing.T) {
	// first_hour 7000, base 3000, durasi 2 jam → 7000 + 3000 = 10000.
	r := Calculate(Input{
		EntryTime: at("2026-07-24 10:00"), ExitTime: at("2026-07-24 12:00"),
		GraceMinutes: 15, BaseRate: 3000, FirstHourRate: 7000,
	})
	if r.Amount != 10000 {
		t.Fatalf("first-hour override: want 10000, got %d", r.Amount)
	}
}

func TestPeakMultiplierIntegerRounding(t *testing.T) {
	// Jumat 18:00 dalam window peak; multiplier 1.50. base 5000 × 1 jam = 5000 → 7500.
	windows := []PeakWindow{{Days: []int{1, 2, 3, 4, 5}, From: "17:00", To: "21:00"}}
	r := Calculate(Input{
		EntryTime: at("2026-07-24 17:30"), ExitTime: at("2026-07-24 18:00"), // Jumat
		GraceMinutes: 15, BaseRate: 5000, PeakMultiplierX100: 150, PeakWindows: windows,
	})
	if !r.IsPeak || r.Amount != 7500 {
		t.Fatalf("peak 1.5x: want peak & 7500, got %+v", r)
	}
}

func TestOutsidePeakNoMultiplier(t *testing.T) {
	windows := []PeakWindow{{Days: []int{1, 2, 3, 4, 5}, From: "17:00", To: "21:00"}}
	r := Calculate(Input{
		EntryTime: at("2026-07-24 09:30"), ExitTime: at("2026-07-24 10:00"), // Jumat pagi
		GraceMinutes: 15, BaseRate: 5000, PeakMultiplierX100: 150, PeakWindows: windows,
	})
	if r.IsPeak || r.Amount != 5000 {
		t.Fatalf("di luar peak: want non-peak & 5000, got %+v", r)
	}
}

func TestDailyCap(t *testing.T) {
	// 10 jam × 5000 = 50000, cap 30000.
	r := Calculate(Input{
		EntryTime: at("2026-07-24 08:00"), ExitTime: at("2026-07-24 18:00"),
		GraceMinutes: 15, BaseRate: 5000, MaxDailyRate: 30000,
	})
	if r.Amount != 30000 || r.Reason != "capped_daily" {
		t.Fatalf("cap harian: want 30000/capped, got %+v", r)
	}
}

func TestLostTicket(t *testing.T) {
	// Tiket hilang → max harian + denda, mengabaikan durasi (§5.5.1).
	r := Calculate(Input{
		EntryTime: at("2026-07-24 10:00"), ExitTime: at("2026-07-24 10:05"),
		GraceMinutes: 15, BaseRate: 5000, MaxDailyRate: 30000,
		LostTicket: true, LostTicketPenalty: 20000,
	})
	if r.Amount != 50000 || r.Reason != "lost_ticket" {
		t.Fatalf("tiket hilang: want 50000, got %+v", r)
	}
}
