package gatesvc

import (
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
)

// Healthcheck internal + kesehatan per gerbang (task 3.4, PRD v3 §13.1).
//
// Kesehatan dinilai per GERBANG, bukan per proses. Lahan dengan empat gerbang yang satu
// controller-nya mati tetap melayani tiga gerbang lain (P8), jadi "edge-api hidup" bukan
// jawaban yang berguna bagi operator — yang ia butuh tahu adalah gerbang mana.

// Ambang & irama healthcheck.
const (
	// DefaultHealthInterval — jarak antar sampel healthcheck internal.
	DefaultHealthInterval = 5 * time.Second

	// DefaultProbeTimeout — batas tunggu satu probe ke goroutine pemilik gerbang.
	//
	// Sengaja jauh lebih pendek dari interval: probe yang menggantung akan menumpuk
	// sampel dan justru membuat healthcheck ikut mati bersama gerbang yang diawasinya.
	DefaultProbeTimeout = 2 * time.Second
)

// Tingkat kesehatan gerbang.
const (
	// HealthOK — gerbang melayani dengan seluruh kemampuannya.
	HealthOK = "ok"
	// HealthDegraded — masih melayani, tetapi tidak utuh (P2: ketersediaan > kesempurnaan).
	HealthDegraded = "degraded"
	// HealthDown — tidak dapat melayani kendaraan.
	HealthDown = "down"
)

// peringkat mengurutkan tingkat kesehatan supaya rollup bisa mengambil yang terburuk.
func peringkat(s string) int {
	switch s {
	case HealthDown:
		return 2
	case HealthDegraded:
		return 1
	default:
		return 0
	}
}

// GateHealth adalah potret kesehatan satu gerbang.
type GateHealth struct {
	Code   string `json:"gate_code"`
	Kind   string `json:"gate_kind"`
	Status string `json:"status"` // ok|degraded|down

	// State kosong bila goroutine pemilik tak menjawab — itu sendiri sudah jadi alasan.
	State    string `json:"state"`
	Menjawab bool   `json:"menjawab"`

	Nyata      bool   `json:"nyata"`
	Controller string `json:"controller,omitempty"` // ONLINE|DISCONNECTED|UNRESPONSIVE

	// Disimulasikan mendaftar perangkat palsu yang masih terpasang di gerbang nyata.
	Disimulasikan []string `json:"disimulasikan,omitempty"`

	// LoopPre/LoopUnder dibuka sebagai diagnostik, BUKAN penentu status: kendaraan di
	// bawah palang adalah keadaan normal yang sekejap. Kasus yang berbahaya (HIGH terlalu
	// lama) sudah dipegang watchdog — menilainya lagi di sini berarti dua pihak menghakimi
	// hal yang sama dengan ambang berbeda.
	LoopPre   bool `json:"loop_pre_high"`
	LoopUnder bool `json:"loop_under_high"`

	// Stats hanya terisi untuk gerbang nyata — riwayat koneksi controller.
	Stats *tcpctl.DeviceStats `json:"controller_stats,omitempty"`

	Alasan        []string  `json:"alasan,omitempty"`
	DiperiksaPada time.Time `json:"diperiksa_pada"`
}

// potretGerbang adalah data yang hanya boleh dibaca goroutine pemilik.
type potretGerbang struct {
	state     string
	loopPre   bool
	loopUnder bool
}

// probe menanyakan potret gerbang lewat inbox, dengan batas waktu.
//
// Batas waktu itu inti dari healthcheck ini. Runner.State() menunggu inbox tanpa batas;
// dipakai di sini ia akan menggantung persis pada gerbang yang paling perlu dilaporkan —
// yang goroutine pemiliknya tersendat. Probe yang menyerah dan MELAPORKAN kesendatan itu
// jauh lebih berguna daripada probe yang ikut membeku.
//
// Kanal hasil dibuffer 1 supaya tugas yang sudah telanjur masuk antrian tetap bisa
// selesai setelah kita menyerah — kalau tidak, goroutine pemilik akan memblokir selamanya
// saat mengirim ke kanal yang tak ada pembacanya, dan probe yang dimaksudkan menyelamatkan
// justru membunuh gerbangnya.
func (r *Runner) probe(timeout time.Duration) (potretGerbang, bool) {
	hasil := make(chan potretGerbang, 1)
	t := tugas{
		jalan: func() {
			var st string
			if r.entry != nil {
				st = string(r.entry.State())
			} else {
				st = string(r.exit.State())
			}
			hasil <- potretGerbang{state: st, loopPre: r.loopPreHigh, loopUnder: r.loopUnderHigh}
		},
		balas: make(chan struct{}),
	}

	tunggu := time.NewTimer(timeout)
	defer tunggu.Stop()

	select {
	case r.inbox <- t:
	case <-r.svc.stop:
		return potretGerbang{}, false
	case <-tunggu.C:
		// Inbox penuh: goroutine pemilik tak menghabiskan antriannya.
		return potretGerbang{}, false
	}

	select {
	case p := <-hasil:
		return p, true
	case <-r.svc.stop:
		return potretGerbang{}, false
	case <-tunggu.C:
		return potretGerbang{}, false
	}
}

// Health menilai kesehatan satu gerbang.
func (r *Runner) Health(timeout time.Duration) GateHealth {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}

	h := GateHealth{
		Code:          r.spec.Code,
		Kind:          string(r.spec.Kind),
		Status:        HealthOK,
		Nyata:         r.dev.Nyata(),
		Controller:    r.dev.Status(),
		Disimulasikan: r.Disimulasikan(),
		DiperiksaPada: time.Now(),
	}

	// Riwayat koneksi controller dibaca tanpa menyentuh inbox — sumbernya Device sendiri,
	// aman lintas goroutine, dan tetap terbaca walau gerbangnya sedang tersendat.
	if r.dev.tcpDev != nil {
		s := r.dev.tcpDev.Stats()
		h.Stats = &s
	}

	p, jawab := r.probe(timeout)
	h.Menjawab = jawab
	if jawab {
		h.State, h.LoopPre, h.LoopUnder = p.state, p.loopPre, p.loopUnder
	} else {
		h.Status = HealthDown
		h.Alasan = append(h.Alasan,
			"goroutine pemilik gerbang tidak menjawab dalam "+timeout.String())
	}

	// Controller mati = palang tak bisa digerakkan. Tak ada gunanya menyebut gerbang ini
	// "melayani sebagian": kendaraan tidak bisa lewat sama sekali.
	if h.Nyata && h.Controller != tcpctl.StatusOnline.String() {
		h.Status = HealthDown
		h.Alasan = append(h.Alasan, "controller "+h.Controller)
	}

	// Perangkat palsu di jalur produksi TIDAK boleh lulus sebagai sehat penuh. Gerbang
	// masuk nyata dengan printer tersimulasi memang membuka palang, tapi pengemudi tak
	// menerima tiket — itu layanan yang tidak utuh, dan operator berhak melihatnya.
	if h.Nyata && len(h.Disimulasikan) > 0 {
		if peringkat(HealthDegraded) > peringkat(h.Status) {
			h.Status = HealthDegraded
		}
		for _, d := range h.Disimulasikan {
			h.Alasan = append(h.Alasan, "perangkat tersimulasi di jalur produksi: "+d)
		}
	}

	return h
}

// ServiceHealth adalah rollup kesehatan seluruh gerbang satu lahan.
type ServiceHealth struct {
	Status string       `json:"status"` // terburuk di antara gerbang
	Gates  []GateHealth `json:"gates"`
}

// Health menilai seluruh gerbang, sesuai urutan sumber konfigurasi.
//
// Gerbang diprobe satu per satu, bukan paralel: jumlah gerbang per lahan kecil (§4.2),
// dan probe berurutan menjaga total waktu terburuk tetap dapat diperkirakan.
func (s *Service) Health(timeout time.Duration) ServiceHealth {
	out := ServiceHealth{Status: HealthOK, Gates: make([]GateHealth, 0, len(s.order))}
	for _, code := range s.order {
		h := s.gates[code].Health(timeout)
		if peringkat(h.Status) > peringkat(out.Status) {
			out.Status = h.Status
		}
		out.Gates = append(out.Gates, h)
	}
	return out
}

// jalankanHealthcheck menyampel kesehatan tiap gerbang dan mengumumkan PERUBAHANnya.
//
// Yang diumumkan hanya perubahan: lahan sepi akan menghasilkan sampel identik setiap
// beberapa detik, dan memancarkan semuanya berarti mengubur event yang berarti di bawah
// aliran tetap yang tak berisi kabar apa pun.
func (s *Service) jalankanHealthcheck(interval, timeout time.Duration) {
	defer s.wg.Done()

	tick := time.NewTicker(interval)
	defer tick.Stop()

	sebelumnya := make(map[string]string, len(s.order))

	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
			for _, code := range s.order {
				select {
				case <-s.stop:
					return
				default:
				}

				h := s.gates[code].Health(timeout)
				lama, pernah := sebelumnya[code]
				if pernah && lama == h.Status {
					continue
				}
				sebelumnya[code] = h.Status

				// Sampel pertama tetap diumumkan: dashboard yang baru menyambung butuh
				// keadaan awal, dan gerbang yang sudah mati sejak startup harus terlihat.
				s.hub.Publish("gate.health.changed", map[string]any{
					"gate_code": h.Code, "gate_kind": h.Kind,
					"status": h.Status, "sebelumnya": lama,
					"state": h.State, "menjawab": h.Menjawab,
					"controller": h.Controller, "alasan": h.Alasan,
				})
			}
		}
	}
}
