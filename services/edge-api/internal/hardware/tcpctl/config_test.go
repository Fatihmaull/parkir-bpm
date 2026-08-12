package tcpctl

import (
	"strings"
	"testing"
)

func TestDefaultGateConfigIkutPRD(t *testing.T) {
	masuk := DefaultGateConfig(GateEntry)
	if masuk.Pins.LoopPre != 1 || masuk.Pins.LoopUnder != 2 {
		t.Fatalf("loop masuk = (%d,%d), want (1,2)", masuk.Pins.LoopPre, masuk.Pins.LoopUnder)
	}
	if masuk.Pins.Button != 3 {
		t.Fatalf("tombol tiket = %d, want 3", masuk.Pins.Button)
	}
	if masuk.Pins.Buzzer != 4 {
		t.Fatalf("buzzer = %d, want 4", masuk.Pins.Buzzer)
	}
	if masuk.Pins.Barrier != 1 || masuk.Pins.GreenLight != 2 || masuk.Pins.RedLight != 3 {
		t.Fatalf("output masuk = %+v", masuk.Pins)
	}

	// Gerbang keluar tak punya tombol tiket maupun buzzer.
	keluar := DefaultGateConfig(GateExit)
	if keluar.Pins.Button != KanalTakDipakai {
		t.Fatalf("tombol keluar = %d, want tak dipakai", keluar.Pins.Button)
	}
	if keluar.Pins.Buzzer != KanalTakDipakai {
		t.Fatalf("buzzer keluar = %d, want tak dipakai", keluar.Pins.Buzzer)
	}

	// Bawaan wajib hold — hanya mode itu yang membuat interlock Edge bisa ditegakkan.
	for _, c := range []GateConfig{masuk, keluar} {
		if c.BarrierMode != BarrierHold {
			t.Fatalf("barrier_mode bawaan = %q, want hold", c.BarrierMode)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("bawaan tidak lolos Validate: %v", err)
		}
	}
}

func TestParseGateConfigKosongPakaiBawaan(t *testing.T) {
	for _, raw := range []string{"", "{}", "null"} {
		got, err := ParseGateConfig(GateEntry, []byte(raw))
		if err != nil {
			t.Fatalf("ParseGateConfig(%q): %v", raw, err)
		}
		if got != DefaultGateConfig(GateEntry) {
			t.Fatalf("ParseGateConfig(%q) = %+v, want bawaan", raw, got)
		}
	}
}

// Kunci yang tak disebut harus tetap memakai bawaan, bukan tersapu jadi nol.
func TestParseGateConfigMenimpaSebagian(t *testing.T) {
	got, err := ParseGateConfig(GateEntry, []byte(`{"barrier_mode":"pulse"}`))
	if err != nil {
		t.Fatalf("ParseGateConfig: %v", err)
	}
	if got.BarrierMode != BarrierPulse {
		t.Fatalf("barrier_mode = %q, want pulse", got.BarrierMode)
	}
	if got.Pins != DefaultGateConfig(GateEntry).Pins {
		t.Fatalf("peta pin ikut berubah: %+v", got.Pins)
	}

	// Idem untuk penimpaan di dalam objek pins.
	got, err = ParseGateConfig(GateEntry, []byte(`{"pins":{"loop_under":4,"spare_in":0}}`))
	if err != nil {
		t.Fatalf("ParseGateConfig: %v", err)
	}
	if got.Pins.LoopUnder != 4 {
		t.Fatalf("loop_under = %d, want 4", got.Pins.LoopUnder)
	}
	if got.Pins.LoopPre != 1 {
		t.Fatalf("loop_pre = %d, want tetap bawaan 1", got.Pins.LoopPre)
	}
	if got.BarrierMode != BarrierHold {
		t.Fatalf("barrier_mode = %q, want tetap bawaan hold", got.BarrierMode)
	}
}

// Input dan output adalah pin fisik terpisah, jadi IN1 dan OUT1 boleh bersamaan.
func TestValidateIzinkanNomorSamaAntaraInputDanOutput(t *testing.T) {
	c := DefaultGateConfig(GateEntry)
	if c.Pins.LoopPre != c.Pins.Barrier {
		t.Fatalf("prasyarat tes: bawaan seharusnya memakai kanal 1 di kedua sisi")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate menolak bawaan yang sah: %v", err)
	}
}

func TestValidateMenolakKonfigurasiBerbahaya(t *testing.T) {
	cases := []struct {
		nama   string
		ubah   func(*GateConfig)
		berisi string
	}{
		{
			nama:   "dua output berbagi relay",
			ubah:   func(c *GateConfig) { c.Pins.GreenLight = c.Pins.Barrier },
			berisi: "dipakai bersama",
		},
		{
			nama:   "dua input berbagi kanal",
			ubah:   func(c *GateConfig) { c.Pins.LoopUnder = c.Pins.LoopPre },
			berisi: "dipakai bersama",
		},
		{
			nama:   "tombol menabrak loop",
			ubah:   func(c *GateConfig) { c.Pins.Button = c.Pins.LoopUnder },
			berisi: "dipakai bersama",
		},
		{
			nama:   "buzzer menabrak lampu",
			ubah:   func(c *GateConfig) { c.Pins.Buzzer = c.Pins.RedLight },
			berisi: "dipakai bersama",
		},
		{
			nama:   "loop bawah tidak diisi",
			ubah:   func(c *GateConfig) { c.Pins.LoopUnder = KanalTakDipakai },
			berisi: "wajib diisi",
		},
		{
			nama:   "palang tidak diisi",
			ubah:   func(c *GateConfig) { c.Pins.Barrier = KanalTakDipakai },
			berisi: "wajib diisi",
		},
		{
			nama:   "kanal di luar rentang",
			ubah:   func(c *GateConfig) { c.Pins.LoopPre = 7 },
			berisi: "di luar rentang",
		},
		{
			nama:   "kanal negatif",
			ubah:   func(c *GateConfig) { c.Pins.Barrier = -1 },
			berisi: "di luar rentang",
		},
		{
			nama:   "mode palang tak dikenal",
			ubah:   func(c *GateConfig) { c.BarrierMode = "auto" },
			berisi: "tidak dikenal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.nama, func(t *testing.T) {
			c := DefaultGateConfig(GateEntry)
			tc.ubah(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate menerima konfigurasi %s", tc.nama)
			}
			if !strings.Contains(err.Error(), tc.berisi) {
				t.Fatalf("pesan error = %q, want memuat %q", err, tc.berisi)
			}
		})
	}
}

func TestValidateIzinkanKanalOpsionalKosong(t *testing.T) {
	c := DefaultGateConfig(GateEntry)
	c.Pins.Button = KanalTakDipakai
	c.Pins.Buzzer = KanalTakDipakai
	c.Pins.SpareIn = KanalTakDipakai
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate menolak kanal opsional kosong: %v", err)
	}
}

func TestParseGateConfigMenolakJSONRusakDanIsiTakSah(t *testing.T) {
	if _, err := ParseGateConfig(GateEntry, []byte(`{"pins":`)); err == nil {
		t.Fatal("JSON rusak seharusnya ditolak")
	}
	if _, err := ParseGateConfig(GateEntry, []byte(`{"barrier_mode":"auto"}`)); err == nil {
		t.Fatal("barrier_mode tak dikenal seharusnya ditolak")
	}
	if _, err := ParseGateConfig(GateExit, []byte(`{"pins":{"green_light":1}}`)); err == nil {
		t.Fatal("bentrok output seharusnya ditolak")
	}
}

func TestBarrierModeValid(t *testing.T) {
	if !BarrierHold.Valid() || !BarrierPulse.Valid() {
		t.Fatal("hold & pulse seharusnya sah")
	}
	for _, m := range []BarrierMode{"", "HOLD", "auto", "trig"} {
		if m.Valid() {
			t.Fatalf("BarrierMode(%q).Valid() = true, want false", m)
		}
	}
}
