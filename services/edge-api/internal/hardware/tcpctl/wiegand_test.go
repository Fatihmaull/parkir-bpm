package tcpctl

import (
	"context"
	"testing"
	"time"
)

func TestParseWiegand(t *testing.T) {
	sah := []struct {
		f    string
		uid  string
		bits int
	}{
		{"W1A2B3C", "1A2B3C", 26},     // W-26, 6 hex
		{"W000000", "000000", 26},     // seluruhnya nol tetap sah
		{"WFFFFFF", "FFFFFF", 26},     // batas atas W-26
		{"W1A2B3C4D", "1A2B3C4D", 34}, // W-34, 8 hex
		{"W00000000", "00000000", 34}, // batas bawah W-34
		{"Wdeadbeef", "DEADBEEF", 34}, // huruf kecil dinormalkan ke besar
		{"Wabcdef", "ABCDEF", 26},     // idem untuk W-26
	}
	for _, tc := range sah {
		uid, bits, ok := ParseWiegand(tc.f)
		if !ok {
			t.Fatalf("ParseWiegand(%q) ok=false, want true", tc.f)
		}
		if uid != tc.uid || bits != tc.bits {
			t.Fatalf("ParseWiegand(%q) = (%q,%d), want (%q,%d)", tc.f, uid, bits, tc.uid, tc.bits)
		}
	}

	takSah := []string{
		"", "W", "W12345", // 5 hex — bukan format yang dikenal
		"W1234567",   // 7 hex
		"W123456789", // 9 hex
		"W12345G",    // G bukan hex
		"W1A2B3 ",    // spasi
		"IN1ON", "PINGOK", "STAT10001000", "OUT1ONOK",
		"1A2B3C", // tanpa awalan W
	}
	for _, f := range takSah {
		if uid, bits, ok := ParseWiegand(f); ok {
			t.Fatalf("ParseWiegand(%q) = (%q,%d,true), want ok=false", f, uid, bits)
		}
	}
}

// Kartu yang sama tidak boleh gagal cocok gara-gara beda kapitalisasi di kabel.
func TestParseWiegandNormalisasiKapitalisasi(t *testing.T) {
	besar, _, ok1 := ParseWiegand("WABCDEF")
	kecil, _, ok2 := ParseWiegand("Wabcdef")
	campur, _, ok3 := ParseWiegand("WAbCdEf")

	if !ok1 || !ok2 || !ok3 {
		t.Fatal("ketiga bentuk seharusnya sah")
	}
	if besar != kecil || kecil != campur {
		t.Fatalf("UID berbeda: %q / %q / %q", besar, kecil, campur)
	}
}

func tungguTap(t *testing.T, d *Device) (uid string) {
	t.Helper()
	select {
	case tap, ok := <-d.RFIDTaps():
		if !ok {
			t.Fatal("kanal RFIDTaps tertutup tak terduga")
		}
		if tap.ReadAt == 0 {
			t.Fatal("ReadAt tidak diisi")
		}
		return tap.UID
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu RFIDTap")
		return ""
	}
}

func TestDeviceMemancarkanTapKartu(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())
	conn := p.terima(t)

	if _, err := conn.Write(frame("W1A2B3C")); err != nil {
		t.Fatalf("perangkat gagal menulis: %v", err)
	}
	if got := tungguTap(t, d); got != "1A2B3C" {
		t.Fatalf("UID = %q, want 1A2B3C", got)
	}

	// Frame mentahnya tetap terlihat di Frames() sebagai bahan diagnostik.
	if got := tungguFrameDevice(t, d); got != "W1A2B3C" {
		t.Fatalf("frame mentah = %q, want W1A2B3C", got)
	}
}

func TestDeviceMemancarkanTapW34(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())
	conn := p.terima(t)

	if _, err := conn.Write(frame("Wdeadbeef")); err != nil {
		t.Fatalf("perangkat gagal menulis: %v", err)
	}
	if got := tungguTap(t, d); got != "DEADBEEF" {
		t.Fatalf("UID = %q, want DEADBEEF", got)
	}
}

// Tap beruntun harus sampai semua — driver tak berhak memutuskan mana yang duplikat.
func TestDeviceMeneruskanTapBeruntun(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())
	conn := p.terima(t)

	uids := []string{"W1A2B3C", "W1A2B3C", "WFFFFFF"}
	for _, u := range uids {
		if _, err := conn.Write(frame(u)); err != nil {
			t.Fatalf("perangkat gagal menulis %s: %v", u, err)
		}
	}
	want := []string{"1A2B3C", "1A2B3C", "FFFFFF"}
	for i, w := range want {
		if got := tungguTap(t, d); got != w {
			t.Fatalf("tap ke-%d = %q, want %q", i, got, w)
		}
	}
}

func TestDeviceMenutupKanalRFIDTaps(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := NewDevice(p.alamat(), opsiDasarUji()...)
	d.Start(context.Background())
	p.terima(t)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	tenggat := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-d.RFIDTaps():
			if !ok {
				return
			}
		case <-tenggat:
			t.Fatal("kanal RFIDTaps tidak tertutup setelah Close")
		}
	}
}
