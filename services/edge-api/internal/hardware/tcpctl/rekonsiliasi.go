package tcpctl

import (
	"context"
	"sync"
	"time"
)

// Rekonsiliasi keadaan setelah blip koneksi (task 3.3, PRD v3 §6.1).
//
// Nama task-nya "antrian perintah", tetapi setelah permukaan perintah A6/A9 dipetakan,
// masalahnya bukan KAPAN mengirim ulang melainkan APA yang dikirim ulang.
//
// Antrian mengulang keputusan dari masa lalu; rekonsiliasi menegaskan keadaan sekarang.
// Yang basi persis adalah masa lalunya: `OUT1OFF` yang tertahan berbahaya bukan karena
// terlambat, melainkan karena ia membawa asumsi "tak ada kendaraan di bawah palang" yang
// sudah kedaluwarsa saat ia akhirnya terkirim.
//
// Karena itu perintah TETAP tidak pernah diantre (lihat ErrNotConnected). Yang disimpan
// hanyalah NIAT — keadaan yang dikehendaki lapisan atas — lalu ditegaskan ulang begitu
// koneksi pulih, dengan syarat keselamatan diperiksa pada detik penegasan itu.
//
// Kelas perintah menentukan perlakuannya, bukan satu TTL seragam:
//
//	TRIG{palang}      kejadian sesaat  → tak pernah ditegaskan ulang; "buka" yang telat
//	                                     3 detik tak punya arti yang aman
//	OUT{palang}ON/OFF keadaan, kritis  → ditegaskan ulang, dengan aturan di bawah
//	OUT{lampu}ON/OFF  keadaan, ringan  → ditegaskan ulang, latest-wins
//	STAT              kueri            → tak pernah; resync (task 3.2) sudah memegangnya
//
// Daftar-putih, bukan daftar-hitam: perintah yang tidak disebut di sini TIDAK ditegaskan
// ulang. Menegaskan ulang perintah adalah bahaya, jadi keanggotaannya lewat pembuktian.

// DefaultMaxOpenIntentAge membatasi usia niat "buka" yang masih layak ditegaskan ulang.
//
// Disamakan dengan timeout `no_show` state machine (45 dtk): di atas itu FSM sendiri sudah
// membatalkan sesi dan memerintahkan tutup, jadi niat buka yang lebih tua hanya mungkin
// berasal dari FSM yang macet. Menegaskannya berarti membuka palang atas keputusan yang
// tak lagi diwakili siapa pun — kendaraan yang diotorisasi sudah lama pergi, dan yang
// menerima palang terbuka adalah kendaraan berikutnya yang belum membayar.
const DefaultMaxOpenIntentAge = 45 * time.Second

// usiaPotretMaks membatasi umur potret STAT yang boleh dipakai menilai palang yatim.
//
// Rekonsiliasi berjalan tepat setelah resync pada koneksi yang sama, jadi potretnya
// semestinya berumur milidetik. Batas ini menjaga dari kasus resync gagal lalu potret
// koneksi SEBELUMNYA dipakai — menutup palang atas dasar bacaan lama adalah menutup buta,
// persis yang dilarang K30.
const usiaPotretMaks = 10 * time.Second

// niatPalang adalah keadaan palang yang dikehendaki lapisan atas.
type niatPalang int

const (
	niatKosong niatPalang = iota // belum ada perintah palang sama sekali
	niatBuka
	niatTutup
)

// niat menyimpan keadaan-diinginkan terakhir untuk satu perangkat.
//
// Kedalamannya SATU dan yang terbaru menang — bukan FIFO. Dua perintah palang yang
// mengantre sudah merupakan kontradiksi; yang lama tak pernah punya nilai, dan menyimpan
// keduanya hanya menciptakan pertanyaan "mana yang dijalankan duluan" yang tak punya
// jawaban benar.
type niat struct {
	mu   sync.Mutex
	apa  niatPalang
	pada time.Time
}

// simpan mencatat kehendak terbaru, menimpa yang sebelumnya.
func (n *niat) simpan(apa niatPalang) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.apa, n.pada = apa, time.Now()
}

func (n *niat) baca() (niatPalang, time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.apa, n.pada
}

// SetReconciler memasang kait yang dijalankan setiap koneksi terbentuk, SETELAH resync
// STAT selesai.
//
// Urutan itu mengikat, bukan kebetulan. Rekonsiliasi penutupan menuntut bukti loop bawah
// LOW (lihat Barrier.tegaskanUlang), dan bukti itu baru ada setelah resync menanamkan
// potret STAT. Dijalankan sebelum resync, setiap rekonsiliasi tutup akan dilewati karena
// status loop "belum diketahui" — aman, tetapi tak pernah berguna.
func (d *Device) SetReconciler(f func(context.Context)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reconciler = f
}

func (d *Device) rekonsiliator() func(context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reconciler
}

// rekonsiliasi menjalankan kait rekonsiliasi bila ada.
func (d *Device) rekonsiliasi(ctx context.Context) {
	if f := d.rekonsiliator(); f != nil {
		f(ctx)
	}
}

// tegaskanUlang menegaskan kembali keadaan yang dikehendaki untuk seluruh perangkat
// gerbang ini. Dipanggil dari goroutine supervisor Device setelah resync.
func (g *Gate) tegaskanUlang(ctx context.Context) {
	// Lampu lebih dulu: murah, tak punya syarat keselamatan, dan memberi pengemudi
	// isyarat yang benar secepat mungkin walau palang masih dinilai.
	g.Light.tegaskanUlang(ctx)
	g.Barrier.tegaskanUlang(ctx)
}

// tegaskanUlang menegaskan kembali posisi palang yang dikehendaki setelah koneksi pulih.
//
// Perintah SELALU dikirim ulang tanpa membandingkannya dengan potret STAT lebih dulu.
// Itu disengaja: potret bisa berasal dari koneksi sebelumnya atau gagal terbaca sama
// sekali (H2), dan melewatkan perintah karena potret basi mengatakan "sudah sesuai"
// adalah kegagalan yang jauh lebih mahal daripada satu relay yang disetel ke nilai yang
// sudah dipegangnya. Kedua perintah idempoten di tingkat relay.
func (b *Barrier) tegaskanUlang(ctx context.Context) {
	// Pada mode pulse Edge tak punya perintah menutup dan "buka" adalah kejadian
	// sesaat — tak ada keadaan yang bisa direkonsiliasi (lihat GateConfig).
	if b.cfg.BarrierMode != BarrierHold {
		return
	}

	apa, pada := b.niat.baca()
	switch apa {
	case niatKosong:
		// Tak ada kehendak yang bisa ditegaskan — tapi belum tentu tak ada yang perlu
		// dikerjakan. Lihat tutupPalangYatim.
		b.tutupPalangYatim(ctx)
		return

	case niatBuka:
		if usia := time.Since(pada); usia > b.maxUsiaNiatBuka {
			b.dev.note(func(s *DeviceStats) { s.ReconcileSkipped++ })
			b.dev.catat(DirTX, "", SeverityWarning,
				"rekonsiliasi buka dilewati: niat berusia "+usia.Truncate(time.Second).String()+
					" — kendaraan yang diotorisasi sudah lama pergi")
			return
		}
		cmd, err := OutOn(b.cfg.Pins.Barrier)
		if err != nil {
			return
		}
		b.jalankan(ctx, cmd, "buka")

	case niatTutup:
		// BUKTI POSITIF, bukan sekadar "tidak diketahui HIGH".
		//
		// Ini lebih ketat daripada periksaInterlock di jalur hidup, dan asimetrinya
		// disengaja. Jalur hidup boleh menutup saat loop tak diketahui karena ia punya
		// bukti yang tak dimiliki rekonsiliator: ia baru saja MELIHAT loop bawah turun.
		// Rekonsiliator tak melihat apa pun — ia hanya tahu lapisan atas ingin tertutup.
		// Menutup atas dasar itu dengan status loop tak diketahui berarti menutup buta.
		//
		// Harganya: di controller yang tak patuh STAT (H2), palang bisa menggantung
		// terbuka setelah reconnect sampai ada event loop nyata. Palang terbuka adalah
		// kerugian pendapatan; palang yang menutup di atas mobil adalah cedera. Kedua
		// kegagalan itu tidak sebanding, dan kita memilih yang bisa diganti uang.
		high, diketahui := b.under.dev.LoopState(b.under.ch)
		if !diketahui || high {
			alasan := "status loop bawah belum diketahui"
			if diketahui {
				alasan = "loop bawah HIGH — kendaraan masih di bawah palang"
			}
			b.dev.note(func(s *DeviceStats) { s.ReconcileSkipped++ })
			b.dev.catat(DirTX, "", SeverityWarning,
				"rekonsiliasi tutup dilewati: "+alasan+" — palang dibiarkan terbuka")
			return
		}
		cmd, err := OutOff(b.cfg.Pins.Barrier)
		if err != nil {
			return
		}
		b.jalankan(ctx, cmd, "tutup")
	}
}

// tutupPalangYatim menutup palang yang ditinggalkan terbuka oleh proses SEBELUMNYA.
//
// Kenapa ada: relay controller mempertahankan posisinya melewati matinya edge-api. Kalau
// proses mati saat palang terbuka — crash, restart deploy, listrik PC padam — palang itu
// tetap terangkat, sementara proses baru memulai state machine dari IDLE dan tak punya
// kehendak apa pun untuk ditegaskan. Tak ada satu pun pihak yang akan menutupnya, dan
// setiap kendaraan berikutnya lewat gratis sampai ada manusia yang menyadarinya.
//
// Diukur sebelum ditulis: setelah restart, relay palang tetap ON dan potret STAT
// melaporkannya apa adanya — pengetahuannya sudah ada, hanya tak ada yang bertindak.
//
// Syaratnya sama ketatnya dengan rekonsiliasi tutup biasa (K30) — bukti POSITIF loop
// bawah LOW — ditambah satu: potret STAT harus segar. Menutup atas dasar potret koneksi
// sebelumnya sama saja menutup buta.
//
// Kenapa menutup, bukan sekadar membunyikan alarm: palang yang menggantung terbuka adalah
// kebocoran pendapatan tanpa batas sekaligus lubang keamanan, sedangkan menutup saat loop
// bawah terbukti LOW secara fisik aman — tak ada yang berada di bawahnya. Alarm tetap
// dibunyikan; ia tak menggantikan penutupan, hanya melengkapinya.
func (b *Barrier) tutupPalangYatim(ctx context.Context) {
	if !b.tutupYatim {
		return
	}

	snap, ada := b.dev.LastStat()
	if !ada || time.Since(snap.At) > usiaPotretMaks {
		return // tak ada bacaan segar — tak bisa membuktikan palang terbuka
	}
	if !snap.Outputs[b.cfg.Pins.Barrier-1] {
		return // palang memang tertutup; tak ada yang yatim
	}

	high, diketahui := b.under.dev.LoopState(b.under.ch)
	if !diketahui || high {
		alasan := "status loop bawah belum diketahui"
		if diketahui {
			alasan = "loop bawah HIGH — kendaraan masih di bawah palang"
		}
		b.dev.note(func(s *DeviceStats) { s.ReconcileSkipped++ })
		b.dev.catat(DirTX, "", SeverityCritical,
			"palang ditemukan TERBUKA tanpa pemilik dan tak dapat ditutup: "+alasan+
				" — perlu diperiksa petugas")
		return
	}

	cmd, err := OutOff(b.cfg.Pins.Barrier)
	if err != nil {
		return
	}
	b.dev.catat(DirTX, cmd, SeverityCritical,
		"palang ditemukan TERBUKA tanpa pemilik setelah proses dimulai — ditutup "+
			"(loop bawah terbukti LOW)")
	b.jalankan(ctx, cmd, "tutup palang yatim")
}

// jalankan mengirim satu perintah rekonsiliasi dan mencatat hasilnya.
func (b *Barrier) jalankan(ctx context.Context, cmd, apa string) {
	if _, err := b.dev.Exec(ctx, cmd); err != nil {
		b.dev.catat(DirTX, cmd, SeverityWarning, "rekonsiliasi "+apa+" gagal: "+err.Error())
		return
	}
	b.dev.note(func(s *DeviceStats) { s.Reconciles++ })
	b.dev.catat(DirTX, cmd, SeverityInfo, "rekonsiliasi "+apa+" setelah koneksi pulih")
}

// tegaskanUlang menyalakan kembali pola lampu statis terakhir.
//
// Pola berkedip sengaja TIDAK dicatat: pengedipnya menyetel ulang kanal tiap 500 ms dan
// karenanya sudah menyembuhkan diri sendiri setelah reconnect. Mencatatnya berarti dua
// pihak menyetel kanal yang sama dengan irama berbeda.
func (l *Light) tegaskanUlang(ctx context.Context) {
	l.mu.Lock()
	pola, ada := l.polaStatis, l.punyaPolaStatis
	l.mu.Unlock()

	if !ada {
		return
	}
	if err := l.Set(ctx, pola); err != nil {
		l.dev.catat(DirTX, "", SeverityWarning, "rekonsiliasi lampu gagal: "+err.Error())
		return
	}
	l.dev.note(func(s *DeviceStats) { s.Reconciles++ })
}
