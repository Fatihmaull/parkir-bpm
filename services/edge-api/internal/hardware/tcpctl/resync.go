package tcpctl

import (
	"context"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
)

// StatSnapshot adalah potret status satu controller dari balasan `STATabcdefgh`
// (PRD v3 §5.5): Inputs[0..3] = Input1..4, Outputs[0..3] = Output1..4.
type StatSnapshot struct {
	Inputs  [ChannelMax]bool
	Outputs [ChannelMax]bool
	At      time.Time
}

// LastStat mengembalikan potret STAT terakhir yang berhasil dibaca dari controller.
// diketahui=false berarti belum ada resync yang berhasil sejak Device dibuat.
//
// Potret ini TIDAK diperbarui otomatis — ia adalah keadaan pada saat koneksi terakhir
// terbentuk, bukan status terkini. Untuk posisi loop yang hidup pakai LoopState, dan
// untuk posisi palang pakai Gate.State yang menanyakan controller saat itu juga.
func (d *Device) LastStat() (StatSnapshot, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastStat, d.punyaLastStat
}

// resyncStat merekonstruksi status controller sekali setiap koneksi terbentuk, baik
// saat startup maupun setelah reconnect (task 3.2, PRD v3 §6.1).
//
// Kenapa perlu: Debouncer.Reset melupakan seluruh nilai yang diakui begitu koneksi
// putus, karena selama Edge buta kendaraan bisa datang atau pergi. Tanpa resync, Edge
// baru mengetahui posisi sebuah loop saat loop itu BERUBAH — kendaraan yang sudah
// berdiri di atas loop sejak sebelum putus tak akan pernah menghasilkan tepi baru, dan
// akan tak terlihat sampai ia pergi.
//
// Dijalankan di goroutine tersendiri, BUKAN sebelum salurkan. Balasan STAT diserahkan
// ke Exec oleh serahkanAck yang dipanggil dari dalam salurkan; memanggil Exec sebelum
// pompa frame berjalan berarti menunggu balasan yang tak akan pernah diambil siapa pun
// sampai batas waktunya habis.
func (d *Device) resyncStat(ctx context.Context) {
	jawab, err := d.Exec(ctx, CmdStat)
	if err != nil {
		d.note(func(s *DeviceStats) { s.StatResyncFailures++ })
		// Sengaja Warning, bukan Critical: §5.5 & §13 mencatat semantik STAT masih
		// menunggu konfirmasi vendor, jadi controller yang belum patuh akan gagal di
		// SETIAP reconnect. Menjadikannya Critical membuat alert kehilangan arti.
		// Konsekuensinya jujur: status loop kembali dipelajari dari event saja,
		// persis perilaku sebelum 3.2.
		d.catat(DirRX, "", SeverityWarning, "resync STAT gagal: "+err.Error())
		return
	}

	inputs, outputs, ok := ParseStat(jawab)
	if !ok {
		d.note(func(s *DeviceStats) { s.StatResyncFailures++ })
		d.catat(DirRX, jawab, SeverityWarning, "resync STAT: balasan tak terbaca")
		return
	}

	d.mu.Lock()
	d.lastStat = StatSnapshot{Inputs: inputs, Outputs: outputs, At: time.Now()}
	d.punyaLastStat = true
	d.stats.StatResyncs++
	d.mu.Unlock()

	d.tanamkanStat(ctx, inputs)
}

// tanamkanStat memasang potret input sebagai dasar penyaring, lalu mengumumkan kanal
// yang berdiri HIGH.
//
// Hanya kanal HIGH yang dikabarkan, dan itu keputusan keselamatan — bukan sekadar
// penghematan derau. Palang menutup pada tepi TURUN loop bawah (PRD v3 §6.2). Kanal
// LOW adalah keadaan istirahat: saat lahan sepi keempat kanal LOW, sehingga
// mengumumkannya berarti memancarkan empat tepi turun palsu pada setiap reconnect —
// perintah tutup untuk kendaraan yang tidak ada. Kanal HIGH adalah kebalikannya:
// keadaan luar biasa yang justru WAJIB diketahui state machine, karena di sanalah
// pengawas BARRIER_BLOCKED dan VEHICLE_STALLED (task 2.4) mulai menghitung.
func (d *Device) tanamkanStat(ctx context.Context, inputs [ChannelMax]bool) {
	var ditanam, diumumkan int
	for i := 0; i < ChannelMax; i++ {
		ch := ChannelMin + i
		if !d.debounce.Seed(ch, inputs[i]) {
			// Kanal ini sudah dipelajari dari event nyata yang lebih baru.
			continue
		}
		ditanam++
		if !inputs[i] {
			continue
		}
		diumumkan++
		d.pancarkanLoopCtx(ctx, hw.LoopEvent{LoopID: ch, High: true, TS: time.Now().UnixMilli()})
	}

	d.note(func(s *DeviceStats) { s.StatSeeded += uint64(ditanam) })
	d.catat(DirRX, "", SeverityInfo, ringkasResync(ditanam, diumumkan))
}

// pancarkanLoopCtx meneruskan satu event seed ke konsumen. Berbeda dari pancarkanLoop,
// ctx koneksi ikut diawasi: konsumen yang berhenti membaca tak boleh menahan goroutine
// resync melewati umur koneksi yang melahirkannya.
func (d *Device) pancarkanLoopCtx(ctx context.Context, ev hw.LoopEvent) {
	select {
	case d.loopEvents <- ev:
	case <-ctx.Done():
	case <-d.done:
	}
}

func ringkasResync(ditanam, diumumkan int) string {
	return "resync STAT: " + itoa(ditanam) + " kanal ditanam, " + itoa(diumumkan) + " HIGH diumumkan"
}

// itoa menghindari fmt untuk jalur yang dipanggil tiap reconnect.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
