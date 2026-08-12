// Package gatesvc menyatukan controller gerbang (Layer 3), simulator perangkat (Layer 2),
// timer nyata, dan event bus menjadi satu service yang dapat dijalankan (PRD §5.6).
//
// Model konkurensi: setiap Fire* diserialisasi oleh satu mutex (pengganti sederhana dari
// "single owner goroutine" §5.6). Event hardware tak-diminta diteruskan oleh goroutine
// terpisah ke Fire*, sehingga state machine tidak pernah memblokir pada I/O.
package gatesvc

import (
	"context"
	"sync"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/sim"
	"github.com/jabar-creative/parkir/edge-api/internal/lpr"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// gtimer — satu deadline aktif per gerbang (arm menggantikan yang sebelumnya).
type gtimer struct {
	mu   sync.Mutex
	t    *time.Timer
	fire func(reason string)
}

func (g *gtimer) arm(d time.Duration, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.t != nil {
		g.t.Stop()
	}
	g.t = time.AfterFunc(d, func() { g.fire(reason) })
}

func (g *gtimer) cancel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.t != nil {
		g.t.Stop()
		g.t = nil
	}
}

// Service menjalankan gerbang masuk & keluar di atas simulator + memstore + hub.
type Service struct {
	mu       sync.Mutex
	entry    *gate.Controller
	exit     *gate.ExitController
	entrySim *sim.Gate
	exitSim  *sim.Gate
	store    *memstore.Store
	hub      *wsbus.Hub
	lpr      lpr.Recognizer
	lprDL    time.Duration
	etimer   gtimer
	xtimer   gtimer
	stop     chan struct{}
}

// Config untuk membangun Service.
type Config struct {
	NodeID      string
	TenantID    string
	SiteID      string
	Site        gate.SiteConfig
	Online      func() bool
	Now         func() time.Time
	Recognizer  lpr.Recognizer // nil → Degraded (LPR tak tersedia, P2)
	LPRDeadline time.Duration
}

// New membangun Service lengkap (in-memory) siap dijalankan.
func New(cfg Config, hub *wsbus.Hub, store *memstore.Store) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Online == nil {
		cfg.Online = func() bool { return true }
	}
	rec := cfg.Recognizer
	if rec == nil {
		rec = lpr.Degraded{EngineVersion: "degraded"} // LPR tak tersedia → UNREAD (P2)
	}
	dl := cfg.LPRDeadline
	if dl <= 0 {
		dl = time.Second // NFR-1.1
	}
	s := &Service{
		entrySim: sim.NewGate(),
		exitSim:  sim.NewGate(),
		store:    store,
		hub:      hub,
		lpr:      rec,
		lprDL:    dl,
		stop:     make(chan struct{}),
	}
	s.etimer.fire = func(reason string) {
		s.FireEntry(gate.Event{Kind: gate.EvTimeout, Reason: reason})
	}
	s.xtimer.fire = func(reason string) {
		s.FireExit(gate.XEvent{Kind: gate.XEvTimeout, Reason: reason})
	}

	s.entry = gate.NewController(gate.Deps{
		Barrier: s.entrySim.Barrier, Light: s.entrySim.Light, Printer: s.entrySim.Printer,
		Store: store, Members: store, Auditor: store, Tickets: store,
		NodeID: cfg.NodeID, TenantID: cfg.TenantID, SiteID: cfg.SiteID, GateLabel: "GATE-IN-01",
		ArmTimeout: s.etimer.arm, CancelTimer: s.etimer.cancel, Emit: hub.Emit(),
	})
	s.exit = gate.NewExitController(gate.ExitDeps{
		Barrier: s.exitSim.Barrier, Light: s.exitSim.Light, Terminal: sim.NewEDC(),
		Store: store, Payments: store, Tariffs: store, Auditor: store,
		Site: cfg.Site, NodeID: cfg.NodeID, TenantID: cfg.TenantID, SiteID: cfg.SiteID,
		GateLabel: "GATE-OUT-01", Online: cfg.Online, Now: cfg.Now,
		ArmTimeout: s.xtimer.arm, CancelTimer: s.xtimer.cancel, Emit: hub.Emit(),
	})
	return s
}

// Start menjalankan goroutine penerus event hardware tak-diminta.
func (s *Service) Start() {
	forwardLoop(s.entrySim.LoopPre.Subscribe(), s.stop, func(e hw.LoopEvent) {
		s.FireEntry(gate.Event{Kind: gate.EvLD1, High: e.High})
		if e.High {
			go s.runLPR("GATE-IN-01") // snapshot LPR async, tidak menggerbang keputusan (§7.3)
		}
	})
	forwardLoop(s.entrySim.LoopPost.Subscribe(), s.stop, func(e hw.LoopEvent) {
		s.FireEntry(gate.Event{Kind: gate.EvLD2, High: e.High})
	})
	forwardRFID(s.entrySim.RFID.Subscribe(), s.stop, func(t hw.RFIDTap) {
		s.FireEntry(gate.Event{Kind: gate.EvRFID, UID: t.UID})
	})
	forwardPrinter(s.entrySim.Printer.Subscribe(), s.stop, func(e hw.PrinterEvent) {
		if e.Kind == "TICKET_TAKEN" {
			s.FireEntry(gate.Event{Kind: gate.EvTicketTaken})
		}
	})
	forwardLoop(s.exitSim.LoopPre.Subscribe(), s.stop, func(e hw.LoopEvent) {
		s.FireExit(gate.XEvent{Kind: gate.XEvLD3, High: e.High})
		if e.High {
			go s.runLPR("GATE-OUT-01")
		}
	})
	forwardLoop(s.exitSim.LoopPost.Subscribe(), s.stop, func(e hw.LoopEvent) {
		s.FireExit(gate.XEvent{Kind: gate.XEvLD4, High: e.High})
	})
}

func (s *Service) Close() { close(s.stop) }

// runLPR memicu pengenalan plat, menulis ocr_logs (SELALU, §7.1), lalu memancarkan lpr.result.
// Hasil tidak pernah menggerbang keputusan gerbang (P2/§7.3).
func (s *Service) runLPR(gateID string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.lprDL)
	defer cancel()
	res, err := s.lpr.Recognize(ctx, nil, gateID)
	if err != nil {
		res = lpr.Result{Verdict: lpr.VerdictUnread, EngineVersion: "error"}
	}
	s.store.WriteOCR(memstore.OcrLog{
		CapturedAt: time.Now(), GateID: gateID, RawText: res.RawText,
		NormalizedPlate: res.NormalizedPlate, Confidence: res.Confidence,
		Verdict: string(res.Verdict), LatencyMs: res.LatencyMs, EngineVersion: res.EngineVersion,
	})
	s.hub.Publish("lpr.result", map[string]any{
		"gate": gateID, "plate": res.NormalizedPlate, "confidence": res.Confidence,
		"verdict": string(res.Verdict),
	})
}

// FireEntry/FireExit menyerahkan satu event ke controller (diserialisasi).
func (s *Service) FireEntry(e gate.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.entry.Handle(context.Background(), e)
}

func (s *Service) FireExit(e gate.XEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.exit.Handle(context.Background(), e)
}

// ── Injeksi event perangkat SINKRON (Field Monitor / POS §12.8 & test) ──
//
// Jalur perangkat nyata bersifat asinkron: perangkat memancarkan event → goroutine
// forwarder → Fire*. Untuk endpoint simulator jalur itu menimbulkan balapan: handler
// HTTP memanggil sim.Drive lalu langsung membaca State() untuk balasannya, padahal
// forwarder mungkin belum sempat jalan — balasan berisi state BASI dan aksi berikutnya
// (mis. identify tepat setelah LD3) bisa tiba saat state machine masih IDLE.
//
// Metode di bawah menyuntik event langsung ke state machine di goroutine pemanggil:
// state sim di-Set tanpa emit, sehingga event tidak terkirim dua kali. Saat kembali,
// state machine dijamin sudah memproses event tersebut.

func (s *Service) DriveEntryLoopPre(high bool) {
	if !s.entrySim.LoopPre.Set(high) {
		return
	}
	s.FireEntry(gate.Event{Kind: gate.EvLD1, High: high})
	if high {
		go s.runLPR("GATE-IN-01") // snapshot LPR async, tidak menggerbang keputusan (§7.3)
	}
}

func (s *Service) DriveEntryLoopPost(high bool) {
	if !s.entrySim.LoopPost.Set(high) {
		return
	}
	s.FireEntry(gate.Event{Kind: gate.EvLD2, High: high})
}

func (s *Service) DriveExitLoopPre(high bool) {
	if !s.exitSim.LoopPre.Set(high) {
		return
	}
	s.FireExit(gate.XEvent{Kind: gate.XEvLD3, High: high})
	if high {
		go s.runLPR("GATE-OUT-01")
	}
}

func (s *Service) DriveExitLoopPost(high bool) {
	if !s.exitSim.LoopPost.Set(high) {
		return
	}
	s.FireExit(gate.XEvent{Kind: gate.XEvLD4, High: high})
}

// TapEntryRFID & TakeEntryTicket: sim RFID/Printer tidak menyimpan state — keduanya
// hanya memancarkan event — jadi cukup diteruskan langsung ke state machine.
func (s *Service) TapEntryRFID(uid string) {
	s.FireEntry(gate.Event{Kind: gate.EvRFID, UID: uid})
}

func (s *Service) TakeEntryTicket() {
	s.FireEntry(gate.Event{Kind: gate.EvTicketTaken})
}

// Accessors untuk HTTP (Field Monitor §12.8) & test.
func (s *Service) EntrySim() *sim.Gate       { return s.entrySim }
func (s *Service) ExitSim() *sim.Gate        { return s.exitSim }
func (s *Service) EntryState() gate.State    { s.mu.Lock(); defer s.mu.Unlock(); return s.entry.State() }
func (s *Service) ExitState() gate.XState    { s.mu.Lock(); defer s.mu.Unlock(); return s.exit.State() }
func (s *Service) ExitAmount() int64         { s.mu.Lock(); defer s.mu.Unlock(); return s.exit.Amount() }
func (s *Service) Store() *memstore.Store    { return s.store }

// ── forwarders ──

func forwardLoop(ch <-chan hw.LoopEvent, stop <-chan struct{}, fn func(hw.LoopEvent)) {
	go func() {
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				fn(e)
			case <-stop:
				return
			}
		}
	}()
}

func forwardRFID(ch <-chan hw.RFIDTap, stop <-chan struct{}, fn func(hw.RFIDTap)) {
	go func() {
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				fn(e)
			case <-stop:
				return
			}
		}
	}()
}

func forwardPrinter(ch <-chan hw.PrinterEvent, stop <-chan struct{}, fn func(hw.PrinterEvent)) {
	go func() {
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				fn(e)
			case <-stop:
				return
			}
		}
	}()
}
