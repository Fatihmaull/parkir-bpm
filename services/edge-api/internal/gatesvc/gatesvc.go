// Package gatesvc menyatukan controller gerbang (Layer 3), perangkat (Layer 2), timer
// nyata, dan event bus menjadi satu service yang dapat dijalankan (PRD §5.6).
//
// Model konkurensi: setiap gerbang dimiliki SATU goroutine (PRD v3 §5.6 "goroutine
// pemilik per device"). Seluruh sentuhan ke state machine — memasukkan event maupun
// membaca state — dilewatkan ke goroutine itu lewat inbox-nya.
//
// Sebelumnya satu mutex tunggal menyerialkan seluruh gerbang. Di lahan dengan banyak
// gerbang itu berarti satu gerbang yang tersendat menahan semua gerbang lain, padahal
// P8 menuntut lahan tetap beroperasi. Sekarang gerbang saling bebas.
package gatesvc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/sim"
	"github.com/jabar-creative/parkir/edge-api/internal/lpr"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// inboxDepth — kedalaman antrian tugas per gerbang.
const inboxDepth = 32

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

// tugas adalah satu pekerjaan yang harus dijalankan oleh goroutine pemilik gerbang.
type tugas struct {
	jalan func()
	balas chan struct{}
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

	// Source memuat daftar gerbang. nil → DefaultSpecs (satu masuk, satu keluar, simulator).
	Source GateSource

	// Ambang pengaman Edge (task 2.4). Nilai 0 memakai bawaan.
	BlockedAfter time.Duration // LD2/LD4 HIGH terlalu lama → BARRIER_BLOCKED
	StalledAfter time.Duration // LD1/LD3 HIGH terlalu lama → VEHICLE_STALLED
}

// Service menjalankan N gerbang di atas perangkat + memstore + hub.
type Service struct {
	gates map[string]*Runner
	order []string // urutan sesuai sumber, agar daftar & log stabil

	store *memstore.Store
	hub   *wsbus.Hub
	lpr   lpr.Recognizer
	lprDL time.Duration

	stop        chan struct{}
	tutupSekali sync.Once
	wg          sync.WaitGroup
}

// New membangun Service dari daftar gerbang yang diberikan Source.
//
// Mengembalikan error bila daftar gerbang tak masuk akal — code kembar, kind tak
// dikenal, atau transport tcp tanpa endpoint. Konfigurasi gerbang yang salah lebih baik
// menghentikan startup daripada memunculkan gerbang yang berperilaku ganjil di lapangan.
func New(cfg Config, hub *wsbus.Hub, store *memstore.Store) (*Service, error) {
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
	src := cfg.Source
	if src == nil {
		src = DefaultSpecs()
	}

	specs, err := src.LoadGates(context.Background())
	if err != nil {
		return nil, fmt.Errorf("gatesvc: gagal memuat daftar gerbang: %w", err)
	}
	if err := ValidateSpecs(specs); err != nil {
		return nil, err
	}

	s := &Service{
		gates: make(map[string]*Runner, len(specs)),
		order: make([]string, 0, len(specs)),
		store: store,
		hub:   hub,
		lpr:   rec,
		lprDL: dl,
		stop:  make(chan struct{}),
	}

	for _, spec := range specs {
		s.gates[spec.Code] = s.buatRunner(spec, cfg)
		s.order = append(s.order, spec.Code)
	}
	return s, nil
}

// buatRunner merangkai satu gerbang beserta controller-nya.
func (s *Service) buatRunner(spec GateSpec, cfg Config) *Runner {
	r := &Runner{
		spec:  spec,
		svc:   s,
		dev:   sim.NewGate(),
		inbox: make(chan tugas, inboxDepth),
	}
	r.wd = newWatchdog(r, cfg.BlockedAfter, cfg.StalledAfter)

	// Timer keselamatan memancarkan event lewat inbox yang sama, jadi ia tetap tunduk
	// pada goroutine pemilik dan tak pernah menyentuh state machine langsung.
	if spec.Kind == KindEntry {
		r.timer.fire = func(reason string) {
			_ = r.FireEntry(gate.Event{Kind: gate.EvTimeout, Reason: reason})
		}
		r.entry = gate.NewController(gate.Deps{
			Barrier: r.dev.Barrier, Light: r.dev.Light, Printer: r.dev.Printer,
			Store: s.store, Members: s.store, Auditor: s.store, Tickets: s.store,
			NodeID: cfg.NodeID, TenantID: cfg.TenantID, SiteID: cfg.SiteID, GateLabel: spec.Code,
			ArmTimeout: r.timer.arm, CancelTimer: r.timer.cancel, Emit: r.emit,
		})
	} else {
		r.timer.fire = func(reason string) {
			_ = r.FireExit(gate.XEvent{Kind: gate.XEvTimeout, Reason: reason})
		}
		r.exit = gate.NewExitController(gate.ExitDeps{
			Barrier: r.dev.Barrier, Light: r.dev.Light, Terminal: sim.NewEDC(),
			Store: s.store, Payments: s.store, Tariffs: s.store, Auditor: s.store,
			Site: cfg.Site, NodeID: cfg.NodeID, TenantID: cfg.TenantID, SiteID: cfg.SiteID,
			GateLabel: spec.Code, Online: cfg.Online, Now: cfg.Now,
			ArmTimeout: r.timer.arm, CancelTimer: r.timer.cancel, Emit: r.emit,
		})
	}
	return r
}

// Start menjalankan goroutine pemilik tiap gerbang beserta penerus event perangkatnya.
func (s *Service) Start() {
	for _, code := range s.order {
		r := s.gates[code]
		s.wg.Add(1)
		go r.milik()
		r.mulaiPenerus()
	}
}

// Close menghentikan seluruh gerbang dan menunggu goroutine pemiliknya selesai.
func (s *Service) Close() {
	s.tutupSekali.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// Specs mengembalikan daftar gerbang sesuai urutan sumbernya.
func (s *Service) Specs() []GateSpec {
	out := make([]GateSpec, 0, len(s.order))
	for _, code := range s.order {
		out = append(out, s.gates[code].spec)
	}
	return out
}

// Gate mencari gerbang berdasarkan code.
func (s *Service) Gate(code string) (*Runner, bool) {
	r, ok := s.gates[code]
	return r, ok
}

// Store memberi akses ke penyimpanan bersama.
func (s *Service) Store() *memstore.Store { return s.store }

// runLPR memicu pengenalan plat, menulis ocr_logs (SELALU, §7.1), lalu memancarkan
// lpr.result berlabel gate_code. Hasil tak pernah menggerbang keputusan gerbang (P2/§7.3).
func (s *Service) runLPR(code string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.lprDL)
	defer cancel()
	res, err := s.lpr.Recognize(ctx, nil, code)
	if err != nil {
		res = lpr.Result{Verdict: lpr.VerdictUnread, EngineVersion: "error"}
	}
	s.store.WriteOCR(memstore.OcrLog{
		CapturedAt: time.Now(), GateID: code, RawText: res.RawText,
		NormalizedPlate: res.NormalizedPlate, Confidence: res.Confidence,
		Verdict: string(res.Verdict), LatencyMs: res.LatencyMs, EngineVersion: res.EngineVersion,
	})
	s.hub.Publish("lpr.result", map[string]any{
		"gate_code": code, "plate": res.NormalizedPlate, "confidence": res.Confidence,
		"verdict": string(res.Verdict),
	})
}

// ── Runner: satu gerbang, satu pemilik ──

// Runner memiliki satu gerbang: hanya goroutine miliknya yang menyentuh state machine.
type Runner struct {
	spec GateSpec
	svc  *Service

	dev   *sim.Gate
	entry *gate.Controller     // terisi bila ENTRY
	exit  *gate.ExitController // terisi bila EXIT

	// Pelacakan loop & pengaman — hanya disentuh goroutine pemilik.
	wd            *watchdog
	loopPreHigh   bool
	loopUnderHigh bool

	timer gtimer
	inbox chan tugas
}

// Spec mengembalikan deskripsi gerbang.
func (r *Runner) Spec() GateSpec { return r.spec }

// Sim memberi akses ke perangkat tersimulasi gerbang ini (Field Monitor & uji).
func (r *Runner) Sim() *sim.Gate { return r.dev }

// emit memancarkan event yang SELALU berlabel gate_code (task 2.2).
//
// Sebelumnya event hanya berlabel "entry"/"exit", sehingga lahan dengan lebih dari satu
// gerbang masuk tak punya cara membedakan asalnya di dashboard maupun di log.
func (r *Runner) emit(name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["gate_code"] = r.spec.Code
	data["gate_kind"] = string(r.spec.Kind)
	r.svc.hub.Publish(name, data)
}

// milik adalah goroutine pemilik gerbang: satu-satunya yang menyentuh state machine.
func (r *Runner) milik() {
	defer r.svc.wg.Done()
	for {
		select {
		case t := <-r.inbox:
			t.jalan()
			close(t.balas)
		case <-r.svc.stop:
			r.timer.cancel()
			r.wd.stop()
			return
		}
	}
}

// lakukan menyerahkan satu pekerjaan ke goroutine pemilik dan menunggu selesainya.
//
// Penantian itu disengaja: endpoint simulator memanggil aksi lalu langsung membalas
// state, dan jawaban yang basi pernah menjadi sumber balapan nyata. Karena penantian
// kini per gerbang, gerbang lain tidak ikut tertahan.
func (r *Runner) lakukan(f func()) error {
	t := tugas{jalan: f, balas: make(chan struct{})}
	select {
	case r.inbox <- t:
	case <-r.svc.stop:
		return ErrBerhenti
	}
	select {
	case <-t.balas:
		return nil
	case <-r.svc.stop:
		return ErrBerhenti
	}
}

// ErrBerhenti dikembalikan bila service sudah dihentikan.
var ErrBerhenti = fmt.Errorf("gatesvc: service sudah berhenti")

// ErrJenisGerbang dikembalikan bila aksi tak sesuai jenis gerbang.
var ErrJenisGerbang = fmt.Errorf("gatesvc: aksi tidak berlaku untuk jenis gerbang ini")

// FireEntry menyerahkan satu event gerbang masuk.
func (r *Runner) FireEntry(e gate.Event) error {
	if r.entry == nil {
		return ErrJenisGerbang
	}
	return r.lakukan(func() {
		_ = r.entry.Handle(context.Background(), e)
		r.wd.amati(r.perbaruiKondisiMasuk(e))
	})
}

// FireExit menyerahkan satu event gerbang keluar.
func (r *Runner) FireExit(e gate.XEvent) error {
	if r.exit == nil {
		return ErrJenisGerbang
	}
	return r.lakukan(func() {
		_ = r.exit.Handle(context.Background(), e)
		r.wd.amati(r.perbaruiKondisiKeluar(e))
	})
}

// State mengembalikan state gerbang dalam bentuk teks.
func (r *Runner) State() string {
	var out string
	_ = r.lakukan(func() {
		if r.entry != nil {
			out = string(r.entry.State())
			return
		}
		out = string(r.exit.State())
	})
	return out
}

// Amount mengembalikan tagihan berjalan gerbang keluar.
func (r *Runner) Amount() (int64, error) {
	if r.exit == nil {
		return 0, ErrJenisGerbang
	}
	var out int64
	if err := r.lakukan(func() { out = r.exit.Amount() }); err != nil {
		return 0, err
	}
	return out, nil
}

// ── Injeksi event perangkat (Field Monitor §12.8 & uji) ──
//
// Jalur perangkat nyata bersifat asinkron: perangkat memancarkan event → penerus →
// state machine. Untuk endpoint simulator jalur itu menimbulkan balapan, karena handler
// HTTP menggerakkan perangkat lalu langsung membaca State() untuk balasannya. Metode di
// bawah menyetel state perangkat TANPA emit lalu menyerahkan event lewat inbox, sehingga
// saat kembali state machine dijamin sudah memprosesnya dan event tak terkirim dua kali.

// DriveLoop menggerakkan loop "pre" (LD1/LD3) atau "post" (LD2/LD4).
func (r *Runner) DriveLoop(which string, high bool) error {
	loop := r.dev.LoopPre
	if which == "post" {
		loop = r.dev.LoopPost
	}
	if !loop.Set(high) {
		return nil // tak ada perubahan
	}

	if r.entry != nil {
		kind := gate.EvLD1
		if which == "post" {
			kind = gate.EvLD2
		}
		if err := r.FireEntry(gate.Event{Kind: kind, High: high}); err != nil {
			return err
		}
	} else {
		kind := gate.XEvLD3
		if which == "post" {
			kind = gate.XEvLD4
		}
		if err := r.FireExit(gate.XEvent{Kind: kind, High: high}); err != nil {
			return err
		}
	}

	// Snapshot LPR hanya pada loop kehadiran, dan asinkron — ia tak pernah
	// menggerbang keputusan gerbang (§7.3).
	if high && which != "post" {
		go r.svc.runLPR(r.spec.Code)
	}
	return nil
}

// TapRFID meniru kartu member di-tap.
func (r *Runner) TapRFID(uid string) error {
	if r.entry != nil {
		return r.FireEntry(gate.Event{Kind: gate.EvRFID, UID: uid})
	}
	return r.FireExit(gate.XEvent{Kind: gate.XEvIdentifyRFID, UID: uid})
}

// TakeTicket meniru pengendara mengambil tiket.
func (r *Runner) TakeTicket() error { return r.FireEntry(gate.Event{Kind: gate.EvTicketTaken}) }

// mulaiPenerus menjalankan penerus event perangkat tak-diminta untuk gerbang ini.
func (r *Runner) mulaiPenerus() {
	stop := r.svc.stop

	teruskanLoop(r.dev.LoopPre.Subscribe(), stop, func(e hw.LoopEvent) {
		if r.entry != nil {
			_ = r.FireEntry(gate.Event{Kind: gate.EvLD1, High: e.High})
		} else {
			_ = r.FireExit(gate.XEvent{Kind: gate.XEvLD3, High: e.High})
		}
		if e.High {
			go r.svc.runLPR(r.spec.Code)
		}
	})
	teruskanLoop(r.dev.LoopPost.Subscribe(), stop, func(e hw.LoopEvent) {
		if r.entry != nil {
			_ = r.FireEntry(gate.Event{Kind: gate.EvLD2, High: e.High})
		} else {
			_ = r.FireExit(gate.XEvent{Kind: gate.XEvLD4, High: e.High})
		}
	})

	if r.entry == nil {
		return // gerbang keluar tak punya pembaca kartu masuk maupun printer
	}
	teruskanRFID(r.dev.RFID.Subscribe(), stop, func(t hw.RFIDTap) {
		_ = r.FireEntry(gate.Event{Kind: gate.EvRFID, UID: t.UID})
	})
	teruskanPrinter(r.dev.Printer.Subscribe(), stop, func(e hw.PrinterEvent) {
		if e.Kind == "TICKET_TAKEN" {
			_ = r.FireEntry(gate.Event{Kind: gate.EvTicketTaken})
		}
	})
}

// ── penerus ──

func teruskanLoop(ch <-chan hw.LoopEvent, stop <-chan struct{}, fn func(hw.LoopEvent)) {
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

func teruskanRFID(ch <-chan hw.RFIDTap, stop <-chan struct{}, fn func(hw.RFIDTap)) {
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

func teruskanPrinter(ch <-chan hw.PrinterEvent, stop <-chan struct{}, fn func(hw.PrinterEvent)) {
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
