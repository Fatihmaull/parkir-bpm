package tcpctl

import (
	"sync"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// DefaultDebounce — jendela stabilitas bawaan untuk input controller
// (PRD v3 §6.1: "debounce input ≥150 ms di Edge, karena controller tak menjamin").
const DefaultDebounce = 150 * time.Millisecond

// Debouncer menyaring pantulan kontak pada input controller sebelum perubahannya
// diakui sebagai LoopEvent.
//
// Cara kerjanya berbasis STABILITAS, bukan pelepasan tepi pertama: perubahan baru
// diakui setelah nilainya bertahan sepanjang jendela. Pilihan ini disengaja dan
// berkaitan dengan keselamatan — palang menutup pada tepi TURUN loop bawah
// (PRD v3 §6.2), jadi satu pantulan LOW palsu yang lolos bisa menutup palang di atas
// kendaraan yang masih lewat. Menunda pengakuan 150 ms tak terasa bagi pengemudi;
// meloloskan pantulan bisa mencederai orang.
//
// Debouncer aman dipakai lintas goroutine.
type Debouncer struct {
	window time.Duration
	emit   func(hw.LoopEvent)

	mu     sync.Mutex
	kanal  map[int]*kanalInput
	closed bool

	// wg melacak timer yang masih tertunda supaya Stop dapat menjamin tak ada emit
	// menyusul setelah ia kembali.
	wg sync.WaitGroup
}

type kanalInput struct {
	stabil      bool // nilai yang sudah diakui
	punyaStabil bool // apakah kanal ini pernah punya nilai yang diakui
	calon       bool // nilai mentah terakhir yang sedang dihitung mundur
	timer       *time.Timer
}

// NewDebouncer membuat penyaring dengan jendela stabilitas window. Nilai <= 0 memakai
// DefaultDebounce. emit dipanggil di luar kunci internal, jadi boleh memblokir.
func NewDebouncer(window time.Duration, emit func(hw.LoopEvent)) *Debouncer {
	if window <= 0 {
		window = DefaultDebounce
	}
	return &Debouncer{window: window, emit: emit, kanal: make(map[int]*kanalInput)}
}

// Observe memasukkan satu pembacaan mentah dari controller.
func (d *Debouncer) Observe(ch int, high bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}

	k := d.kanal[ch]
	if k == nil {
		k = &kanalInput{}
		d.kanal[ch] = k
	}

	if k.punyaStabil && k.stabil == high {
		// Pantulan yang kembali ke nilai yang sudah diakui: batalkan perubahan tertunda.
		d.hentikanTimer(k)
		k.calon = high
		return
	}
	if k.timer != nil && k.calon == high {
		// Sudah menghitung mundur untuk nilai ini. Jangan diperpanjang — nilainya
		// memang bertahan sejak penegasan pertama.
		return
	}

	k.calon = high
	d.hentikanTimer(k)
	d.wg.Add(1)
	k.timer = time.AfterFunc(d.window, func() {
		defer d.wg.Done()
		d.stabilkan(ch, high)
	})
}

// Seed menanamkan potret STAT sebagai nilai awal kanal ch tanpa menunggu jendela
// stabilitas, dan melaporkan apakah nilai itu benar-benar dipasang.
//
// Hanya kanal yang BELUM punya nilai diakui yang diisi. Potret STAT diminta tepat
// setelah koneksi pulih, tetapi balasannya bisa tiba SETELAH event INxON/INxOFF nyata
// lebih dulu masuk lewat jalur frame — artinya potret itu lebih tua daripada apa yang
// sudah diketahui. Menimpanya akan memundurkan status ke masa lalu.
//
// Hitungan mundur yang sedang berjalan sengaja TIDAK dibatalkan: ia berasal dari
// pembacaan nyata yang lebih baru, dan stabilkan akan menimpa nilai seed ini begitu
// jendelanya habis. Seed hanya memberi dasar sementara, bukan kata akhir.
func (d *Debouncer) Seed(ch int, high bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}

	k := d.kanal[ch]
	if k == nil {
		k = &kanalInput{}
		d.kanal[ch] = k
	}
	if k.punyaStabil {
		return false
	}

	k.stabil = high
	k.punyaStabil = true
	// Jangan sentuh calon bila ada timer berjalan — itu milik pembacaan nyata yang
	// sedang dihitung mundur dan harus tetap menang.
	if k.timer == nil {
		k.calon = high
	}
	return true
}

// Stable melaporkan nilai kanal yang sudah diakui.
func (d *Debouncer) Stable(ch int) (high, diketahui bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := d.kanal[ch]
	if k == nil || !k.punyaStabil {
		return false, false
	}
	return k.stabil, true
}

// Reset melupakan seluruh nilai yang sudah diakui dan membatalkan hitungan mundur
// yang tertunda.
//
// Dipanggil saat koneksi putus: selama Edge buta, kendaraan bisa datang atau pergi,
// sehingga keyakinan lama tentang posisi loop tidak lagi punya dasar. Melupakannya
// membuat pembacaan pertama setelah pulih selalu diakui ulang, alih-alih ditelan
// karena "sama dengan nilai lama". Rekonstruksi status yang sesungguhnya lewat STAT
// dikerjakan Device.resyncStat (task 3.2), yang mengisi ulang kanal lewat Seed.
func (d *Debouncer) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, k := range d.kanal {
		d.hentikanTimer(k)
	}
	d.kanal = make(map[int]*kanalInput)
}

// Stop mematikan penyaring dan menunggu sampai tak ada emit yang mungkin menyusul.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	for _, k := range d.kanal {
		d.hentikanTimer(k)
	}
	d.mu.Unlock()

	d.wg.Wait()
}

// hentikanTimer dipanggil dengan d.mu tertahan.
func (d *Debouncer) hentikanTimer(k *kanalInput) {
	if k.timer == nil {
		return
	}
	// Stop() true berarti timer batal sebelum sempat jalan, jadi callback-nya tak akan
	// pernah memanggil Done() — kita yang harus menutup hitungannya.
	if k.timer.Stop() {
		d.wg.Done()
	}
	k.timer = nil
}

// stabilkan mengakui nilai calon bila ia masih bertahan saat jendela habis.
func (d *Debouncer) stabilkan(ch int, high bool) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	k := d.kanal[ch]
	if k == nil || k.calon != high {
		d.mu.Unlock()
		return // nilainya berubah lagi sebelum sempat stabil
	}
	// Nilai yang sudah diakui tidak diumumkan dua kali. Lewat Observe saja hal ini
	// tak mungkin terjadi (Observe membatalkan timer begitu nilainya kembali ke yang
	// diakui), tetapi Seed dapat menanamkan nilai yang sama saat hitungan mundur nyata
	// masih berjalan — tanpa penjaga ini state machine menerima dua tepi naik untuk
	// satu kendaraan.
	sama := k.punyaStabil && k.stabil == high
	k.stabil = high
	k.punyaStabil = true
	k.timer = nil
	emit := d.emit
	d.mu.Unlock()

	if emit != nil && !sama {
		emit(hw.LoopEvent{LoopID: ch, High: high, TS: time.Now().UnixMilli()})
	}
}
