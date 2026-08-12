package tcpctl

import (
	"encoding/json"
	"fmt"
)

// GateKind membedakan gerbang masuk dan keluar (kolom `gates.kind`).
type GateKind string

const (
	GateEntry GateKind = "ENTRY"
	GateExit  GateKind = "EXIT"
)

// BarrierMode menentukan cara Edge menggerakkan palang (PRD v3 §6.2).
type BarrierMode string

const (
	// BarrierHold — OUT1ON menaikkan, OUT1OFF menurunkan. Edge memegang penuh timing
	// tutup lewat sensor. Ini bawaan, dan satu-satunya mode yang membuat interlock
	// keselamatan (P4) benar-benar bisa ditegakkan Edge.
	BarrierHold BarrierMode = "hold"

	// BarrierPulse — TRIG1 memberi pulsa 1 detik lalu mati sendiri, untuk palang tipe
	// "pulse-to-open, auto-close-mekanis". Waktu menutup ditentukan mekanisme palang,
	// BUKAN Edge, sehingga Edge tak dapat menahan penutupan saat loop bawah HIGH.
	BarrierPulse BarrierMode = "pulse"
)

// Valid melaporkan apakah mode dikenal.
func (m BarrierMode) Valid() bool { return m == BarrierHold || m == BarrierPulse }

// KanalTakDipakai menandai kanal opsional yang tidak dipasang di gerbang ini.
const KanalTakDipakai = 0

// PinMap memetakan fungsi logis ke nomor kanal fisik controller (PRD v3 §5.6).
//
// Peta ini adalah keputusan desain v3 yang dapat ditimpa per gerbang lewat
// `gates.config`, karena wiring di lapangan tidak selalu mengikuti bawaan.
type PinMap struct {
	// Input
	LoopPre   int `json:"loop_pre"`   // LD1/LD3 — loop kehadiran sebelum palang
	LoopUnder int `json:"loop_under"` // LD2/LD4 — loop di bawah palang; dasar interlock P4
	Button    int `json:"button"`     // tombol ambil tiket; 0 bila tak ada
	SpareIn   int `json:"spare_in"`   // sensor tamper / pintu box; 0 bila tak ada

	// Output
	Barrier    int `json:"barrier"`     // palang
	GreenLight int `json:"green_light"` // lampu hijau
	RedLight   int `json:"red_light"`   // lampu merah
	Buzzer     int `json:"buzzer"`      // buzzer; 0 bila tak ada
}

// GateConfig adalah isi `gates.config` untuk satu gerbang.
type GateConfig struct {
	Pins        PinMap      `json:"pins"`
	BarrierMode BarrierMode `json:"barrier_mode"`
}

// DefaultGateConfig mengembalikan peta pin bawaan v3 (PRD §5.6) untuk jenis gerbang.
//
// Nomor kanalnya kebetulan sama antara masuk dan keluar; yang berbeda adalah kanal
// mana yang benar-benar terpasang — gerbang keluar tak punya tombol tiket maupun
// buzzer, dan scanner QR-nya dibaca PC Kasir, bukan controller.
func DefaultGateConfig(kind GateKind) GateConfig {
	cfg := GateConfig{
		Pins: PinMap{
			LoopPre:    1,
			LoopUnder:  2,
			SpareIn:    4,
			Barrier:    1,
			GreenLight: 2,
			RedLight:   3,
		},
		BarrierMode: BarrierHold,
	}
	if kind == GateEntry {
		cfg.Pins.Button = 3
		cfg.Pins.Buzzer = 4
	}
	return cfg
}

// ParseGateConfig membaca `gates.config` di atas bawaan untuk jenis gerbang tersebut.
// Kunci yang tak disebut JSON tetap memakai nilai bawaan.
func ParseGateConfig(kind GateKind, raw []byte) (GateConfig, error) {
	cfg := DefaultGateConfig(kind)

	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return GateConfig{}, fmt.Errorf("tcpctl: gates.config tidak dapat dibaca: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return GateConfig{}, err
	}
	return cfg, nil
}

// Validate memeriksa kewarasan peta pin.
//
// Pemeriksaan bentrok kanal bukan sekadar kerapian: dua fungsi yang menunjuk relay
// output yang sama berarti menyalakan lampu ikut menggerakkan palang. Salah ketik di
// `gates.config` tidak boleh berubah menjadi palang yang bergerak sendiri, jadi
// konfigurasi seperti itu ditolak sejak awal alih-alih ditemukan di lapangan.
func (c GateConfig) Validate() error {
	if !c.BarrierMode.Valid() {
		return fmt.Errorf("tcpctl: barrier_mode %q tidak dikenal (hold|pulse)", c.BarrierMode)
	}

	wajibIn := map[string]int{"loop_pre": c.Pins.LoopPre, "loop_under": c.Pins.LoopUnder}
	opsionalIn := map[string]int{"button": c.Pins.Button, "spare_in": c.Pins.SpareIn}
	wajibOut := map[string]int{"barrier": c.Pins.Barrier, "green_light": c.Pins.GreenLight, "red_light": c.Pins.RedLight}
	opsionalOut := map[string]int{"buzzer": c.Pins.Buzzer}

	if err := periksaKanal(wajibIn, true); err != nil {
		return err
	}
	if err := periksaKanal(opsionalIn, false); err != nil {
		return err
	}
	if err := periksaKanal(wajibOut, true); err != nil {
		return err
	}
	if err := periksaKanal(opsionalOut, false); err != nil {
		return err
	}

	if err := periksaBentrok("input", wajibIn, opsionalIn); err != nil {
		return err
	}
	return periksaBentrok("output", wajibOut, opsionalOut)
}

// periksaKanal memastikan tiap kanal berada di rentang yang didukung controller.
func periksaKanal(kanal map[string]int, wajib bool) error {
	for nama, ch := range kanal {
		if ch == KanalTakDipakai {
			if wajib {
				return fmt.Errorf("tcpctl: kanal %q wajib diisi", nama)
			}
			continue
		}
		if ch < ChannelMin || ch > ChannelMax {
			return fmt.Errorf("tcpctl: kanal %q = %d, di luar rentang %d..%d", nama, ch, ChannelMin, ChannelMax)
		}
	}
	return nil
}

// periksaBentrok menolak dua fungsi yang menunjuk kanal fisik yang sama.
func periksaBentrok(golongan string, kelompok ...map[string]int) error {
	pemilik := make(map[int]string)
	for _, kanal := range kelompok {
		for nama, ch := range kanal {
			if ch == KanalTakDipakai {
				continue
			}
			if lain, ada := pemilik[ch]; ada {
				return fmt.Errorf("tcpctl: kanal %s %d dipakai bersama oleh %q dan %q", golongan, ch, lain, nama)
			}
			pemilik[ch] = nama
		}
	}
	return nil
}
