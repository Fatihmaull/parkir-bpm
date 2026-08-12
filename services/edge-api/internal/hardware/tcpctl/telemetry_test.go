package tcpctl

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRingMenyimpanUrutLamaKeBaru(t *testing.T) {
	r := NewRing(4)
	for _, p := range []string{"a", "b", "c"} {
		r.Add(TelemetryEntry{Payload: p})
	}

	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("Len = %d, want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Payload != want {
			t.Fatalf("entri %d = %q, want %q", i, got[i].Payload, want)
		}
	}
}

// Ukuran tetap adalah syarat: Edge berjalan berbulan-bulan tanpa diawasi.
func TestRingMenggusurYangTerlama(t *testing.T) {
	r := NewRing(3)
	for _, p := range []string{"a", "b", "c", "d", "e"} {
		r.Add(TelemetryEntry{Payload: p})
	}

	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("Len = %d, want 3 (kapasitas)", len(got))
	}
	for i, want := range []string{"c", "d", "e"} {
		if got[i].Payload != want {
			t.Fatalf("entri %d = %q, want %q", i, got[i].Payload, want)
		}
	}
	if r.Cap() != 3 {
		t.Fatalf("Cap = %d, want 3", r.Cap())
	}
}

func TestRingKapasitasBawaan(t *testing.T) {
	for _, c := range []int{0, -5} {
		if got := NewRing(c).Cap(); got != DefaultTelemetryCap {
			t.Fatalf("NewRing(%d).Cap() = %d, want %d", c, got, DefaultTelemetryCap)
		}
	}
}

func TestRingAmanDipakaiBersamaan(t *testing.T) {
	r := NewRing(64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Add(TelemetryEntry{Payload: "x"})
				_ = r.Snapshot()
			}
		}()
	}
	wg.Wait()
	if r.Len() != 64 {
		t.Fatalf("Len = %d, want 64", r.Len())
	}
}

func TestSeverityDanDirectionString(t *testing.T) {
	if SeverityInfo.String() != "info" || SeverityWarning.String() != "warning" || SeverityCritical.String() != "critical" {
		t.Fatal("nama severity tidak sesuai")
	}
	if DirTX.String() != "TX" || DirRX.String() != "RX" {
		t.Fatal("nama arah tidak sesuai")
	}
}

func TestDeviceMencatatTXdanRX(t *testing.T) {
	d, _, conn := siapkanExec(t)

	if _, err := d.Exec(context.Background(), CmdStat); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, err := conn.Write(frame("IN1ON")); err != nil {
		t.Fatalf("perangkat gagal menulis: %v", err)
	}
	tungguFrameDevice(t, d)

	jejak := d.Telemetry()
	var adaTX, adaRXBalasan, adaRXEvent bool
	for _, e := range jejak {
		switch {
		case e.Dir == DirTX && e.Payload == CmdStat:
			adaTX = true
		case e.Dir == DirRX && e.Payload == "STAT10001000":
			adaRXBalasan = true
		case e.Dir == DirRX && e.Payload == "IN1ON":
			adaRXEvent = true
		}
	}
	if !adaTX || !adaRXBalasan || !adaRXEvent {
		t.Fatalf("jejak kurang lengkap (TX=%v RXbalasan=%v RXevent=%v): %+v",
			adaTX, adaRXBalasan, adaRXEvent, jejak)
	}
}

// Keepalive 1 Hz akan mengubur kejadian nyata bila ikut dicatat.
func TestDeviceTidakMencatatKeepaliveSecaraBawaan(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(),
		WithReconnectBackoff(backoffUji()),
		WithPingInterval(20*time.Millisecond),
		WithMaxMissedPing(50),
	)
	conn := p.terima(t)
	mulaiPenjawab(conn)

	tungguSampai(t, 3*time.Second, func() bool { return d.Stats().Pongs >= 5 }, "beberapa PINGOK")

	for _, e := range d.Telemetry() {
		if e.Severity == SeverityInfo && (e.Payload == CmdPing || e.Payload == RespPingOK) {
			t.Fatalf("lalu lintas keepalive tercatat padahal bawaan tidak: %+v", e)
		}
	}
}

func TestDeviceMencatatKeepaliveBilaDiminta(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(),
		WithReconnectBackoff(backoffUji()),
		WithPingInterval(20*time.Millisecond),
		WithMaxMissedPing(50),
		WithTelemetryKeepalive(true),
	)
	conn := p.terima(t)
	mulaiPenjawab(conn)

	tungguSampai(t, 3*time.Second, func() bool { return d.Stats().Pongs >= 3 }, "beberapa PINGOK")

	var adaPing bool
	for _, e := range d.Telemetry() {
		if e.Payload == CmdPing || e.Payload == RespPingOK {
			adaPing = true
			break
		}
	}
	if !adaPing {
		t.Fatal("keepalive tidak tercatat padahal diminta")
	}
}

// PRD v3 §6.1: audit dipicu untuk severity >= warning.
func TestAuditHookDipicuUntukWarningKeAtas(t *testing.T) {
	var (
		mu    sync.Mutex
		masuk []TelemetryEntry
	)
	rekam := func(e TelemetryEntry) {
		mu.Lock()
		defer mu.Unlock()
		masuk = append(masuk, e)
	}

	// Alamat mati memaksa dial gagal berulang — sumber entri warning yang andal.
	mulaiDevice(t, alamatMati(t), WithAuditHook(rekam))

	tungguSampai(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(masuk) > 0
	}, "audit menerima entri")

	mu.Lock()
	defer mu.Unlock()
	for _, e := range masuk {
		if e.Severity < SeverityWarning {
			t.Fatalf("audit menerima severity %v, want >= warning", e.Severity)
		}
	}
}

func TestAuditHookTidakDipicuUntukLaluLintasNormal(t *testing.T) {
	var (
		mu    sync.Mutex
		masuk []TelemetryEntry
	)
	rekam := func(e TelemetryEntry) {
		mu.Lock()
		defer mu.Unlock()
		masuk = append(masuk, e)
	}

	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), WithPingInterval(0), WithAckTimeout(200*time.Millisecond), WithAuditHook(rekam))
	conn := p.terima(t)
	mulaiController(conn)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")

	if _, err := d.Exec(context.Background(), CmdStat); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range masuk {
		if e.Severity < SeverityWarning {
			t.Fatalf("audit menerima entri normal: %+v", e)
		}
	}
}

// Perintah yang gagal karena terputus harus meninggalkan jejak, bukan hilang diam-diam.
func TestDeviceMencatatPerintahSaatTerputus(t *testing.T) {
	d := mulaiDevice(t, alamatMati(t), WithAckTimeout(50*time.Millisecond))

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().DialFailures > 0
	}, "dial gagal")

	_ = d.Send(context.Background(), CmdPing)

	var adaCatatan bool
	for _, e := range d.Telemetry() {
		if e.Dir == DirTX && e.Payload == CmdPing && e.Severity >= SeverityWarning {
			adaCatatan = true
			break
		}
	}
	if !adaCatatan {
		t.Fatalf("perintah saat terputus tidak tercatat: %+v", d.Telemetry())
	}
}

func TestWithTelemetryCap(t *testing.T) {
	d := NewDevice("127.0.0.1:1", WithTelemetryCap(8))
	if got := d.telemetry.Cap(); got != 8 {
		t.Fatalf("kapasitas = %d, want 8", got)
	}
	def := NewDevice("127.0.0.1:1", WithTelemetryCap(0))
	if got := def.telemetry.Cap(); got != DefaultTelemetryCap {
		t.Fatalf("kapasitas bawaan = %d, want %d", got, DefaultTelemetryCap)
	}
}
