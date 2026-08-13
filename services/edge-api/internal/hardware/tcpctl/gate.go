package tcpctl

import (
	"context"
	"fmt"
	"sync"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// Perangkat Layer-2 di atas tcpctl memenuhi kontrak interface v2 yang sama dengan
// simulator, sehingga state machine gerbang tidak perlu berubah (PRD v3 §6.1).
var (
	_ hw.Barrier        = (*Barrier)(nil)
	_ hw.LoopDetector   = (*Loop)(nil)
	_ hw.IndicatorLight = (*Light)(nil)
	_ hw.RFIDReader     = (*Reader)(nil)
)

const (
	// blinkInterval — periode nyala/padam pola berkedip.
	blinkInterval = 500 * time.Millisecond
	// subBuffer — kedalaman antrian tiap subscriber.
	subBuffer = 64
)

// Gate menyusun perangkat Layer-2 satu gerbang di atas satu Device, memakai peta pin
// dari gates.config (task 1.9).
//
// Susunan fieldnya sengaja mencerminkan sim.Gate supaya lapisan di atas dapat menukar
// simulator dengan perangkat nyata tanpa mengubah bentuk pemakaian (P7).
type Gate struct {
	dev *Device
	cfg GateConfig

	Barrier  *Barrier
	Light    *Light
	LoopPre  *Loop // LD1/LD3 — loop kehadiran sebelum palang
	LoopPost *Loop // LD2/LD4 — loop di bawah palang; dasar interlock (cfg.Pins.LoopUnder)
	RFID     *Reader

	tutupSekali sync.Once
	selesai     chan struct{}
	wg          sync.WaitGroup
}

// NewGate merangkai perangkat Layer-2 dan mulai menyalurkan event dari Device.
//
// NewGate juga memasang penjaga interlock pada Device: perintah menutup palang ditolak
// selama loop bawah HIGH, dan karena penjaga dievaluasi ulang tiap percobaan retry
// (task 1.4), larangan itu tetap berlaku sepanjang rentetan pengiriman.
func NewGate(dev *Device, cfg GateConfig) (*Gate, error) {
	if dev == nil {
		return nil, fmt.Errorf("tcpctl: Device tidak boleh nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	g := &Gate{dev: dev, cfg: cfg, selesai: make(chan struct{})}
	g.LoopPre = &Loop{dev: dev, ch: cfg.Pins.LoopPre}
	g.LoopPost = &Loop{dev: dev, ch: cfg.Pins.LoopUnder}
	g.RFID = &Reader{}
	g.Light = &Light{dev: dev, green: cfg.Pins.GreenLight, red: cfg.Pins.RedLight}
	g.Barrier = &Barrier{
		dev: dev, cfg: cfg, under: g.LoopPost,
		maxUsiaNiatBuka: DefaultMaxOpenIntentAge,
		tutupYatim:      true,
	}

	if cfg.BarrierMode == BarrierHold {
		perintahTutup, err := OutOff(cfg.Pins.Barrier)
		if err != nil {
			return nil, err
		}
		dev.SetCommandGuard(func(cmd string) error {
			if cmd != perintahTutup {
				return nil
			}
			return g.Barrier.periksaInterlock()
		})
	}

	// Rekonsiliasi keadaan setelah blip koneksi (task 3.3). Device memanggilnya
	// setiap koneksi terbentuk, SETELAH resync STAT — lihat SetReconciler.
	dev.SetReconciler(g.tegaskanUlang)

	g.wg.Add(2)
	go g.salurkanLoop()
	go g.salurkanTap()
	return g, nil
}

// SetCloseOrphanedBarrier menyalakan/mematikan penutupan palang yang ditinggalkan proses
// sebelumnya (lihat Barrier.tutupPalangYatim). Bawaannya menyala.
//
// Katup darurat, sejalan dengan WithStatResync: bila di lapangan ternyata ada lahan yang
// memang menghendaki palang tetap terangkat melewati restart — mis. mode gerbang terbuka
// saat jam sibuk — mematikannya di sini lebih jujur daripada menambal di tempat lain.
func (g *Gate) SetCloseOrphanedBarrier(aktif bool) { g.Barrier.tutupYatim = aktif }

// SetMaxOpenIntentAge mengatur usia maksimum niat "buka" yang masih layak ditegaskan
// ulang setelah reconnect. Nilai <= 0 mengembalikannya ke bawaan.
func (g *Gate) SetMaxOpenIntentAge(d time.Duration) {
	if d <= 0 {
		d = DefaultMaxOpenIntentAge
	}
	g.Barrier.maxUsiaNiatBuka = d
}

// Config mengembalikan konfigurasi gerbang yang dipakai.
func (g *Gate) Config() GateConfig { return g.cfg }

// Close menghentikan penyaluran event dan pola berkedip. Device-nya sendiri tidak
// ikut ditutup — pemiliknya yang mengatur itu.
func (g *Gate) Close() error {
	g.tutupSekali.Do(func() {
		// Lepas dulu: rekonsiliasi yang menyusul tak boleh menyentuh gerbang yang
		// sudah ditutup.
		g.dev.SetReconciler(nil)
		close(g.selesai)
		g.Light.hentikanKedip()
	})
	g.wg.Wait()
	return nil
}

// salurkanLoop membagikan LoopEvent yang sudah ter-debounce ke loop yang bersangkutan.
func (g *Gate) salurkanLoop() {
	defer g.wg.Done()
	for {
		select {
		case ev, ok := <-g.dev.LoopEvents():
			if !ok {
				return
			}
			switch ev.LoopID {
			case g.LoopPre.ch:
				g.LoopPre.sebarkan(g.selesai, ev)
			case g.LoopPost.ch:
				g.LoopPost.sebarkan(g.selesai, ev)
			}
			// Kanal lain (tombol, cadangan) bukan loop detector; state machine
			// menerimanya lewat jalur lain.
		case <-g.selesai:
			return
		}
	}
}

func (g *Gate) salurkanTap() {
	defer g.wg.Done()
	for {
		select {
		case tap, ok := <-g.dev.RFIDTaps():
			if !ok {
				return
			}
			g.RFID.sebarkan(g.selesai, tap)
		case <-g.selesai:
			return
		}
	}
}

// ── Barrier ──

// Barrier menggerakkan palang lewat relay output sesuai mode di gates.config.
type Barrier struct {
	dev   *Device
	cfg   GateConfig
	under *Loop

	// niat menyimpan posisi palang yang dikehendaki lapisan atas, untuk ditegaskan
	// ulang setelah koneksi pulih (task 3.3). Dicatat SEBELUM perintah dikirim: yang
	// perlu diingat adalah kehendaknya, bukan berhasil-tidaknya pengiriman.
	niat            niat
	maxUsiaNiatBuka time.Duration

	// tutupYatim menyalakan penutupan palang yang ditinggalkan proses sebelumnya.
	tutupYatim bool
}

// Open menaikkan palang.
func (b *Barrier) Open(ctx context.Context) error {
	b.niat.simpan(niatBuka)

	var (
		cmd string
		err error
	)
	if b.cfg.BarrierMode == BarrierPulse {
		cmd, err = Trig(b.cfg.Pins.Barrier)
	} else {
		cmd, err = OutOn(b.cfg.Pins.Barrier)
	}
	if err != nil {
		return err
	}
	_, err = b.dev.Exec(ctx, cmd)
	return err
}

// Close menurunkan palang, dengan interlock keselamatan P4 ditegakkan di Edge.
//
// Pemeriksaan interlock dilakukan dua kali dan itu disengaja: sekali di sini supaya
// penolakan langsung terbaca pemanggil, dan sekali lagi lewat penjaga perintah di
// dalam gelang retry, karena loop bawah dapat berubah menjadi HIGH di antara dua
// percobaan pengiriman.
//
// Pada mode pulse, Edge tidak memiliki perintah menutup: palang turun oleh mekanisme
// auto-close-nya sendiri (PRD v3 §6.2). Close menjadi tanpa-operasi yang berhasil —
// bukan karena aman, melainkan karena tidak ada yang bisa diperintahkan. Konsekuensinya
// interlock TIDAK dapat ditegakkan perangkat lunak pada mode itu; karena itulah bawaan
// gates.config adalah hold.
func (b *Barrier) Close(ctx context.Context) error {
	if b.cfg.BarrierMode == BarrierPulse {
		return nil
	}
	// Niat dicatat walau interlock menolak: yang dikehendaki lapisan atas tetap
	// "tertutup", dan rekonsiliasi nanti memeriksa ulang syaratnya sendiri.
	b.niat.simpan(niatTutup)

	if err := b.periksaInterlock(); err != nil {
		return err
	}
	cmd, err := OutOff(b.cfg.Pins.Barrier)
	if err != nil {
		return err
	}
	_, err = b.dev.Exec(ctx, cmd)
	return err
}

// periksaInterlock menolak penutupan selama loop di bawah palang HIGH (P4, PRD v3 §6.2).
//
// Bila loop bawah belum pernah terbaca stabil (misalnya tepat setelah reconnect),
// penutupan TIDAK diblokir. Memblokir tanpa dasar akan membuat palang menggantung
// terbuka setiap kali koneksi pulih, dan palang yang tak pernah menutup adalah
// kegagalan operasional tersendiri.
//
// Resync STAT (task 3.2) mempersempit jendela "belum diketahui" itu menjadi satu kali
// perjalanan perintah setelah koneksi terbentuk, tetapi TIDAK menghapusnya: resync bisa
// gagal, dan controller yang belum patuh §5.5 tak akan pernah mengisinya. Jadi jangan
// mengubah cabang ini menjadi "blokir bila tak diketahui" dengan anggapan 3.2 selalu
// berhasil — anggapan itu tidak dijamin.
func (b *Barrier) periksaInterlock() error {
	if high, diketahui := b.under.dev.LoopState(b.under.ch); diketahui && high {
		return hw.ErrSafetyInterlock
	}
	return nil
}

// State melaporkan posisi palang berdasarkan STAT dari controller.
//
// Pada mode pulse relay hanya menyala sekejap, sehingga bacaan relay tidak mewakili
// posisi palang; State mengembalikan BarrierUnknown alih-alih menyesatkan.
func (b *Barrier) State(ctx context.Context) (hw.BarrierState, error) {
	if b.cfg.BarrierMode == BarrierPulse {
		return hw.BarrierUnknown, nil
	}
	jawab, err := b.dev.Exec(ctx, CmdStat)
	if err != nil {
		return hw.BarrierUnknown, err
	}
	_, outputs, ok := ParseStat(jawab)
	if !ok {
		return hw.BarrierUnknown, fmt.Errorf("tcpctl: balasan STAT tak terbaca: %q", jawab)
	}
	if outputs[b.cfg.Pins.Barrier-1] {
		return hw.BarrierOpen, nil
	}
	return hw.BarrierClosed, nil
}

// ── Loop detector ──

// Loop adalah satu loop detector pada kanal input tertentu.
type Loop struct {
	dev *Device
	ch  int

	mu   sync.Mutex
	subs []chan hw.LoopEvent
}

// Channel mengembalikan nomor kanal input yang diwakili loop ini.
func (l *Loop) Channel() int { return l.ch }

// Read melaporkan posisi loop saat ini.
//
// Sumbernya adalah nilai ter-debounce yang sudah dipegang driver, bukan tanya-jawab ke
// controller: interlock memanggil ini di jalur kritis, dan jawaban lokal yang tak bisa
// gagal lebih berguna di situ daripada round-trip yang bisa habis waktu. Bila belum ada
// pembacaan stabil, barulah STAT ditanyakan.
func (l *Loop) Read(ctx context.Context) (bool, error) {
	if high, diketahui := l.dev.LoopState(l.ch); diketahui {
		return high, nil
	}
	jawab, err := l.dev.Exec(ctx, CmdStat)
	if err != nil {
		return false, err
	}
	inputs, _, ok := ParseStat(jawab)
	if !ok {
		return false, fmt.Errorf("tcpctl: balasan STAT tak terbaca: %q", jawab)
	}
	return inputs[l.ch-1], nil
}

// Subscribe membuka aliran perubahan loop ini. Boleh dipanggil beberapa kali.
func (l *Loop) Subscribe() <-chan hw.LoopEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := make(chan hw.LoopEvent, subBuffer)
	l.subs = append(l.subs, ch)
	return ch
}

// sebarkan mengirim event ke seluruh subscriber.
//
// Pengiriman memblokir sampai diterima (atau gerbang ditutup) — berbeda dari simulator
// yang membuang saat konsumen lambat. Di perangkat nyata, transisi loop yang hilang
// berarti palang salah menilai ada-tidaknya kendaraan, dan itu menyangkut P4.
func (l *Loop) sebarkan(selesai <-chan struct{}, ev hw.LoopEvent) {
	l.mu.Lock()
	subs := append([]chan hw.LoopEvent(nil), l.subs...)
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		case <-selesai:
			return
		}
	}
}

// ── Indicator light ──

// Light mengendalikan lampu hijau & merah lewat dua relay output.
type Light struct {
	dev           *Device
	green, red    int
	mu            sync.Mutex
	batalkanKedip context.CancelFunc
	kedipWG       sync.WaitGroup

	// Pola statis terakhir, untuk ditegaskan ulang setelah reconnect (task 3.3).
	// Pola berkedip tak dicatat — pengedipnya sudah menyembuhkan diri tiap 500 ms.
	polaStatis      hw.LightPattern
	punyaPolaStatis bool
}

// Set menerapkan pola lampu.
//
// PatternYellowBlink ditolak: peta pin v3 (§5.6) tidak menyediakan kanal kuning, dan
// menyalakan warna lain diam-diam akan memberi pengemudi isyarat yang keliru.
func (l *Light) Set(ctx context.Context, p hw.LightPattern) error {
	l.hentikanKedip()

	switch p {
	case hw.PatternOff, hw.PatternGreen, hw.PatternRed:
		l.ingatPolaStatis(p)
		return l.setel(ctx, p == hw.PatternGreen, p == hw.PatternRed)
	case hw.PatternRedBlink:
		l.lupakanPolaStatis()
		if err := l.setel(ctx, false, true); err != nil {
			return err
		}
		l.mulaiKedip(l.red)
		return nil
	case hw.PatternYellowBlink:
		return fmt.Errorf("tcpctl: pola kuning tak tersedia — peta pin tidak punya kanal lampu kuning")
	default:
		return fmt.Errorf("tcpctl: pola lampu %d tidak dikenal", p)
	}
}

// ingatPolaStatis mencatat pola yang perlu ditegaskan ulang setelah reconnect.
func (l *Light) ingatPolaStatis(p hw.LightPattern) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.polaStatis, l.punyaPolaStatis = p, true
}

// lupakanPolaStatis dipanggil saat pola berkedip mengambil alih kanal.
func (l *Light) lupakanPolaStatis() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.punyaPolaStatis = false
}

// setel menyalakan/mematikan kedua lampu sesuai kehendak.
func (l *Light) setel(ctx context.Context, hijau, merah bool) error {
	if err := l.setelSatu(ctx, l.green, hijau); err != nil {
		return err
	}
	return l.setelSatu(ctx, l.red, merah)
}

func (l *Light) setelSatu(ctx context.Context, ch int, nyala bool) error {
	if ch == KanalTakDipakai {
		return nil
	}
	var (
		cmd string
		err error
	)
	if nyala {
		cmd, err = OutOn(ch)
	} else {
		cmd, err = OutOff(ch)
	}
	if err != nil {
		return err
	}
	_, err = l.dev.Exec(ctx, cmd)
	return err
}

// mulaiKedip menjalankan pengedip untuk satu kanal sampai pola berikutnya disetel.
func (l *Light) mulaiKedip(ch int) {
	if ch == KanalTakDipakai {
		return
	}
	ctx, batal := context.WithCancel(context.Background())

	l.mu.Lock()
	l.batalkanKedip = batal
	l.mu.Unlock()

	l.kedipWG.Add(1)
	go func() {
		defer l.kedipWG.Done()
		t := time.NewTicker(blinkInterval)
		defer t.Stop()

		nyala := true
		for {
			select {
			case <-t.C:
				nyala = !nyala
				// Kegagalan diabaikan: saat controller terputus pengedip tak bisa
				// berbuat apa-apa, dan itu bukan alasan menghentikan pola. Tick
				// berikutnya mencoba lagi setelah koneksi pulih.
				_ = l.setelSatu(ctx, ch, nyala)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// hentikanKedip menghentikan pengedip yang sedang berjalan, bila ada.
func (l *Light) hentikanKedip() {
	l.mu.Lock()
	batal := l.batalkanKedip
	l.batalkanKedip = nil
	l.mu.Unlock()

	if batal != nil {
		batal()
	}
	l.kedipWG.Wait()
}

// ── RFID reader ──

// Reader membagikan tap kartu ke pelanggannya.
type Reader struct {
	mu   sync.Mutex
	subs []chan hw.RFIDTap
}

// Subscribe membuka aliran tap kartu. Boleh dipanggil beberapa kali.
func (r *Reader) Subscribe() <-chan hw.RFIDTap {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan hw.RFIDTap, subBuffer)
	r.subs = append(r.subs, ch)
	return ch
}

func (r *Reader) sebarkan(selesai <-chan struct{}, tap hw.RFIDTap) {
	r.mu.Lock()
	subs := append([]chan hw.RFIDTap(nil), r.subs...)
	r.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- tap:
		case <-selesai:
			return
		}
	}
}
