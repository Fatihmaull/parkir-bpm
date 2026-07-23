// Package fare — mesin tarif (PRD §5.5.2). Dihitung di Edge (P1), tidak pernah di Cloud.
// Semua uang BIGINT rupiah utuh; pengali peak diterapkan dengan aritmetika integer + pembulatan,
// tidak pernah float pada uang (D6).
package fare

import "time"

// PeakWindow — jendela jam sibuk (PRD §5.5.2 peak_windows JSONB).
type PeakWindow struct {
	Days []int  // 1=Senin .. 7=Minggu (ISO weekday)
	From string // "17:00"
	To   string // "21:00"
}

// Input mesin tarif. Waktu diasumsikan sudah dalam timezone site.
type Input struct {
	EntryTime time.Time
	ExitTime  time.Time

	GraceMinutes     int
	MemberCoversSite bool // member aktif & mencakup site → tarif 0 (§5.5.2)

	BaseRate      int64 // per jam
	FirstHourRate int64 // 0 = sama dengan BaseRate (schema tariffs.first_hour_rate)

	PeakMultiplierX100 int64 // NUMERIC(4,2) → integer ratusan: 100=1.00, 150=1.50
	PeakWindows        []PeakWindow

	MaxDailyRate      int64 // 0 = tanpa cap
	LostTicket        bool
	LostTicketPenalty int64
}

// Result rincian perhitungan (untuk POS §12.9 & audit).
type Result struct {
	DurationMin  int
	ChargedHours int
	IsPeak       bool
	Free         bool
	Reason       string
	Amount       int64
}

// Calculate menghitung tarif sesuai PRD §5.5.2.
//
// Catatan desain: §5.5.2 menuliskan subtotal = tarif_dasar × jam × pengali. Field
// tariffs.first_hour_rate (§11.2) diperlakukan sebagai override jam pertama bila diisi:
// subtotal = first_hour_rate + base_rate×(jam-1), lalu × pengali. Bila FirstHourRate=0,
// perilaku identik dengan formula dasar §5.5.2.
func Calculate(in Input) Result {
	durMin := durationMinutes(in.EntryTime, in.ExitTime)
	res := Result{DurationMin: durMin}

	// Tiket hilang: tarif maksimum harian + denda (§5.5.1). Mendahului grace/member.
	if in.LostTicket {
		base := in.MaxDailyRate
		res.Amount = base + in.LostTicketPenalty
		res.Reason = "lost_ticket"
		return res
	}

	if durMin <= in.GraceMinutes {
		res.Free = true
		res.Reason = "grace_period"
		return res
	}
	if in.MemberCoversSite {
		res.Free = true
		res.Reason = "member"
		return res
	}

	hours := ceilDiv(durMin, 60)
	res.ChargedHours = hours
	res.IsPeak = isPeak(in.ExitTime, in.PeakWindows)

	firstHour := in.FirstHourRate
	if firstHour == 0 {
		firstHour = in.BaseRate
	}
	subtotal := firstHour + in.BaseRate*int64(hours-1)

	mult := in.PeakMultiplierX100
	if mult <= 0 {
		mult = 100
	}
	if res.IsPeak {
		// Bulatkan ke rupiah terdekat: (subtotal*mult + 50) / 100.
		subtotal = (subtotal*mult + 50) / 100
	}

	if in.MaxDailyRate > 0 && subtotal > in.MaxDailyRate {
		subtotal = in.MaxDailyRate
		res.Reason = "capped_daily"
	}
	res.Amount = subtotal
	return res
}

// durationMinutes = ceil((exit - entry) / 60 detik). Durasi negatif dianggap 0.
func durationMinutes(entry, exit time.Time) int {
	secs := int(exit.Sub(entry).Seconds())
	if secs <= 0 {
		return 0
	}
	return ceilDiv(secs, 60)
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// isPeak mengecek apakah t jatuh dalam salah satu peak window.
func isPeak(t time.Time, windows []PeakWindow) bool {
	wd := int(t.Weekday()) // Go: Minggu=0..Sabtu=6
	iso := wd
	if iso == 0 {
		iso = 7 // konversi ke ISO: Senin=1..Minggu=7
	}
	minOfDay := t.Hour()*60 + t.Minute()
	for _, w := range windows {
		if !containsDay(w.Days, iso) {
			continue
		}
		from, ok1 := parseHM(w.From)
		to, ok2 := parseHM(w.To)
		if !ok1 || !ok2 {
			continue
		}
		if minOfDay >= from && minOfDay < to {
			return true
		}
	}
	return false
}

func containsDay(days []int, d int) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

// parseHM mem-parse "HH:MM" → menit sejak tengah malam.
func parseHM(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}
