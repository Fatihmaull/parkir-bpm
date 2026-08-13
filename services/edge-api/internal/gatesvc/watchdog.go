package gatesvc

import (
	"context"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// Ambang waktu pengaman Edge (PRD v3 §6.2, PRD v2 §5.4.x).
//
// Auto-close TIDAK ada di sini: state machine sudah menutup palang sendiri lewat timeout
// `no_show` (bawaan 45 dtk, lihat gate.Timeouts) — lebih ketat daripada ">60 dtk" yang
// disebut PRD v3, jadi syaratnya sudah terpenuhi. Menambah timer kedua untuk hal yang
// sama hanya akan menghasilkan dua pihak yang memerintah palang.
const (
	DefaultBlockedAfter = 120 * time.Second
	DefaultStalledAfter = 120 * time.Second
)

// Jenis alert yang dipancarkan pengaman.
const (
	AlertBarrierBlocked      = "BARRIER_BLOCKED"
	AlertVehicleStalled      = "VEHICLE_STALLED"
	AlertUnauthorizedPassage = "UNAUTHORIZED_PASSAGE"
)

// kondisi adalah potret gerbang sesaat setelah satu event diproses.
type kondisi struct {
	loopPreHigh   bool // LD1/LD3 — loop kehadiran sebelum palang
	loopUnderHigh bool // LD2/LD4 — loop di bawah palang
	palangTerbuka bool // state machine sedang OPEN/CLEARING
}

// watchdog mengawasi kondisi berbahaya yang tidak diwakili state machine.
//
// Seluruh metodenya dipanggil dari goroutine pemilik gerbang, jadi tak butuh kunci.
// Callback timer berjalan di goroutine sendiri dan hanya menyentuh hub (aman lintas
// goroutine) serta perangkat lampu.
type watchdog struct {
	r            *Runner
	blockedAfter time.Duration
	stalledAfter time.Duration

	tBlocked *time.Timer
	tStalled *time.Timer
}

func newWatchdog(r *Runner, blockedAfter, stalledAfter time.Duration) *watchdog {
	if blockedAfter <= 0 {
		blockedAfter = DefaultBlockedAfter
	}
	if stalledAfter <= 0 {
		stalledAfter = DefaultStalledAfter
	}
	return &watchdog{r: r, blockedAfter: blockedAfter, stalledAfter: stalledAfter}
}

// amati mengevaluasi ulang kondisi berbahaya setelah satu event diproses.
func (w *watchdog) amati(k kondisi) {
	// Kendaraan tersangkut di bawah palang — butuh intervensi manusia (PRD v2 §5.4:
	// "LD2 HIGH > 120 detik" → critical).
	w.pantau(&w.tBlocked, k.loopUnderHigh, w.blockedAfter, func() {
		w.r.alert(AlertBarrierBlocked, "critical",
			"loop di bawah palang HIGH melebihi ambang — kendaraan diduga tersangkut")
	})

	// Kendaraan diam di loop kehadiran tanpa menyelesaikan siklus — mogok, atau loop rusak.
	w.pantau(&w.tStalled, k.loopPreHigh && !k.palangTerbuka, w.stalledAfter, func() {
		w.r.alert(AlertVehicleStalled, "warning",
			"loop kehadiran HIGH melebihi ambang tanpa siklus selesai — kendaraan mogok atau loop rusak")

		// PENYIMPANGAN TERCATAT dari PRD v2 §5.4: spesifikasi menyebut "lampu kuning
		// blink", tetapi peta pin v3 §5.6 tidak menyediakan kanal lampu kuning — hanya
		// hijau dan merah. Memakai merah berkedip adalah isyarat terdekat yang jujur;
		// menyalakan hijau atau membiarkan lampu apa adanya akan menyesatkan pengemudi.
		// Bila lampu kuning ternyata ada di lapangan, tambahkan kanalnya ke gates.config
		// dan ganti pola di sini.
		_ = w.r.dev.Light.Set(context.Background(), hw.PatternRedBlink)
	})
}

// pantau menyalakan timer saat kondisi mulai berlaku dan mematikannya saat berhenti.
//
// Timer sengaja TIDAK di-restart selama kondisinya bertahan: ambangnya dihitung sejak
// kondisi mulai, bukan sejak event terakhir. Kalau di-restart tiap event, loop yang
// dilewati kendaraan berat (banyak pantulan) tak akan pernah mencapai ambang.
func (w *watchdog) pantau(t **time.Timer, aktif bool, after time.Duration, fire func()) {
	if aktif {
		if *t == nil {
			*t = time.AfterFunc(after, fire)
		}
		return
	}
	if *t != nil {
		(*t).Stop()
		*t = nil
	}
}

// periksaPelanggaran memeriksa lintasan tanpa otorisasi: loop di bawah palang naik
// padahal palang tak pernah dibuka untuk siapa pun (PRD v2 §5.4 → critical).
//
// Ini tidak memakai timer — begitu terdeteksi, sudah terlambat untuk dicegah; yang bisa
// dilakukan hanya mencatat dan membunyikan alert.
func (w *watchdog) periksaPelanggaran(naik bool, palangTerbuka bool) {
	if naik && !palangTerbuka {
		w.r.alert(AlertUnauthorizedPassage, "critical",
			"loop di bawah palang naik tanpa otorisasi — palang diduga ditembus atau dibuka paksa")
	}
}

// stop mematikan seluruh timer pengaman.
func (w *watchdog) stop() {
	if w.tBlocked != nil {
		w.tBlocked.Stop()
		w.tBlocked = nil
	}
	if w.tStalled != nil {
		w.tStalled.Stop()
		w.tStalled = nil
	}
}

// alert memancarkan peringatan operasional berlabel gerbang.
func (r *Runner) alert(jenis, severity, pesan string) {
	r.svc.hub.Publish("alert.raised", map[string]any{
		"type": jenis, "severity": severity, "message": pesan,
		"gate_code": r.spec.Code, "gate_kind": string(r.spec.Kind),
	})
}

// perbaruiKondisi memperbarui pelacakan loop dari satu event masuk, lalu mengembalikan
// potret kondisi terkini. Hanya dipanggil dari goroutine pemilik.
func (r *Runner) perbaruiKondisiMasuk(e gate.Event) kondisi {
	switch e.Kind {
	case gate.EvLD1:
		r.loopPreHigh = e.High
	case gate.EvLD2:
		naik := e.High && !r.loopUnderHigh
		r.loopUnderHigh = e.High
		if naik {
			r.wd.periksaPelanggaran(true, r.palangTerbukaMasuk())
		}
	}
	return kondisi{
		loopPreHigh:   r.loopPreHigh,
		loopUnderHigh: r.loopUnderHigh,
		palangTerbuka: r.palangTerbukaMasuk(),
	}
}

func (r *Runner) perbaruiKondisiKeluar(e gate.XEvent) kondisi {
	switch e.Kind {
	case gate.XEvLD3:
		r.loopPreHigh = e.High
	case gate.XEvLD4:
		naik := e.High && !r.loopUnderHigh
		r.loopUnderHigh = e.High
		if naik {
			r.wd.periksaPelanggaran(true, r.palangTerbukaKeluar())
		}
	}
	return kondisi{
		loopPreHigh:   r.loopPreHigh,
		loopUnderHigh: r.loopUnderHigh,
		palangTerbuka: r.palangTerbukaKeluar(),
	}
}

// palangTerbuka* melaporkan apakah lintasan sedang sah — palang terbuka atau kendaraan
// yang sudah diotorisasi sedang melewatinya.
func (r *Runner) palangTerbukaMasuk() bool {
	st := r.entry.State()
	return st == gate.StateOpen || st == gate.StateClearing || st == gate.StateOpening
}

func (r *Runner) palangTerbukaKeluar() bool {
	st := r.exit.State()
	return st == gate.XOpen || st == gate.XClearing || st == gate.XOpening
}
