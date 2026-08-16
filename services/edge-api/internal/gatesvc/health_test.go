package gatesvc

import (
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

// svcKesehatan menyiapkan Service dengan irama healthcheck yang dipercepat.
func svcKesehatan(t *testing.T, src GateSource, interval, probe time.Duration) (*Service, *wsbus.Hub) {
	t.Helper()
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site:               gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source:             src,
		HealthInterval:     interval,
		HealthProbeTimeout: probe,
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc, hub
}

func cariKesehatan(t *testing.T, kes ServiceHealth, code string) GateHealth {
	t.Helper()
	for _, h := range kes.Gates {
		if h.Code == code {
			return h
		}
	}
	t.Fatalf("kesehatan tak memuat %s: %+v", code, kes.Gates)
	return GateHealth{}
}

// Lahan simulator penuh sehat: tiap gerbang menjawab, dan rollup ikut ok.
func TestKesehatanLahanSimulatorSehat(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())

	kes := svc.Health(time.Second)
	if kes.Status != HealthOK {
		t.Fatalf("rollup = %q, want %q (%+v)", kes.Status, HealthOK, kes.Gates)
	}
	if len(kes.Gates) != 3 {
		t.Fatalf("jumlah gerbang = %d, want 3", len(kes.Gates))
	}

	// Urutan kesehatan mengikuti sumber, sama seperti Specs() — halaman status yang
	// gerbangnya melompat-lompat tiap refresh tak bisa dibaca.
	want := []string{"GATE-IN-01", "GATE-IN-02", "GATE-OUT-01"}
	for i, w := range want {
		if kes.Gates[i].Code != w {
			t.Fatalf("gerbang %d = %q, want %q", i, kes.Gates[i].Code, w)
		}
	}

	h := cariKesehatan(t, kes, "GATE-IN-01")
	if !h.Menjawab {
		t.Fatalf("gerbang sim harus menjawab: %+v", h)
	}
	if h.State != string(gate.StateIdle) {
		t.Fatalf("state = %q, want %q", h.State, gate.StateIdle)
	}
	if h.Nyata {
		t.Fatalf("gerbang sim tak boleh mengaku nyata: %+v", h)
	}
	if len(h.Alasan) != 0 {
		t.Fatalf("gerbang sehat tak boleh punya alasan: %v", h.Alasan)
	}
}

// Inti task 3.4: healthcheck harus MELAPORKAN gerbang yang goroutine pemiliknya tersendat,
// bukan ikut menggantung bersamanya.
//
// Runner.State() menunggu inbox tanpa batas waktu; kalau Health() memakainya, permintaan
// /health pada gerbang yang macet tak akan pernah dijawab — persis pada gerbang yang paling
// perlu dilaporkan.
func TestKesehatanTurunSaatPemilikTersendat(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())
	r, _ := svc.Gate("GATE-IN-01")

	lepas := make(chan struct{})
	r.inbox <- tugas{jalan: func() { <-lepas }, balas: make(chan struct{})}

	mulai := time.Now()
	h := r.Health(150 * time.Millisecond)
	lama := time.Since(mulai)

	if h.Menjawab {
		t.Fatalf("gerbang tersendat tak boleh dilaporkan menjawab: %+v", h)
	}
	if h.Status != HealthDown {
		t.Fatalf("status = %q, want %q", h.Status, HealthDown)
	}
	if h.State != "" {
		t.Fatalf("state harus kosong saat pemilik tak menjawab, got %q", h.State)
	}
	if len(h.Alasan) == 0 {
		t.Fatalf("gerbang down harus menyertakan alasan")
	}
	if lama > 2*time.Second {
		t.Fatalf("probe menggantung %v — batas waktu tidak berlaku", lama)
	}

	// Gerbang lain tidak ikut terseret: pemilik per gerbang harus benar-benar saling bebas.
	lain := svc.Health(time.Second)
	if h2 := cariKesehatan(t, lain, "GATE-IN-02"); h2.Status != HealthOK {
		t.Fatalf("gerbang tetangga ikut turun: %+v", h2)
	}

	close(lepas)
}

// Probe yang menyerah TIDAK boleh membunuh gerbang yang diprobenya.
//
// Tugas probe tetap tertinggal di inbox setelah kita menyerah. Bila kanal hasilnya tak
// dibuffer, goroutine pemilik akan memblokir selamanya saat mengirim ke kanal yang sudah
// tak ada pembacanya — healthcheck yang dimaksudkan mendeteksi kemacetan justru menjadi
// penyebabnya.
func TestProbeYangMenyerahTakMembunuhGerbang(t *testing.T) {
	svc, _ := svcDengan(t, duaMasukSatuKeluar())
	r, _ := svc.Gate("GATE-IN-01")

	lepas := make(chan struct{})
	r.inbox <- tugas{jalan: func() { <-lepas }, balas: make(chan struct{})}

	if h := r.Health(100 * time.Millisecond); h.Menjawab {
		t.Fatalf("probe seharusnya menyerah: %+v", h)
	}
	close(lepas) // pemilik kini memproses tugas probe yang sudah ditinggalkan

	// Gerbang harus kembali melayani sepenuhnya.
	selesai := make(chan string, 1)
	go func() { selesai <- r.State() }()
	select {
	case st := <-selesai:
		if st != string(gate.StateIdle) {
			t.Fatalf("state setelah pulih = %q, want %q", st, gate.StateIdle)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gerbang tetap macet setelah probe ditinggalkan — pemilik terblokir")
	}

	if h := r.Health(time.Second); h.Status != HealthOK || !h.Menjawab {
		t.Fatalf("kesehatan setelah pulih = %+v, want ok", h)
	}
}

// Gerbang nyata yang controller-nya tak tersambung = tak bisa melayani kendaraan sama
// sekali → down, bukan degraded.
func TestKesehatanGerbangNyataTanpaController(t *testing.T) {
	// Port yang dijamin tak ada yang mendengarkan: dial akan terus gagal.
	src := StaticSource{{
		Code: "GATE-IN-01", Kind: KindEntry,
		Transport: TransportTCP, Endpoint: "127.0.0.1:1",
	}}
	svc, _ := svcKesehatan(t, src, time.Hour, 500*time.Millisecond)

	r, _ := svc.Gate("GATE-IN-01")
	h := r.Health(time.Second)

	if !h.Nyata {
		t.Fatalf("gerbang bertransport tcp harus mengaku nyata: %+v", h)
	}
	if h.Status != HealthDown {
		t.Fatalf("status = %q, want %q (controller=%q)", h.Status, HealthDown, h.Controller)
	}
	if h.Controller == tcpctl.StatusOnline.String() {
		t.Fatalf("controller mustahil ONLINE ke port mati: %+v", h)
	}
	// Goroutine pemilik sendiri baik-baik saja — yang mati controller-nya, dan alasannya
	// harus membedakan keduanya.
	if !h.Menjawab {
		t.Fatalf("goroutine pemilik seharusnya tetap menjawab: %+v", h)
	}
	if h.Stats == nil {
		t.Fatalf("gerbang nyata harus melaporkan riwayat koneksi controller")
	}
}

// Perangkat palsu di gerbang nyata tak boleh lulus sebagai sehat penuh: palang membuka,
// tapi pengemudi tak menerima tiket. Itu degraded, dan wajib terlihat (P3).
func TestKesehatanDegradedKarenaPerangkatTersimulasi(t *testing.T) {
	svc, _ := svcNyata(t, KindEntry)
	r, _ := svc.Gate("GATE-IN-01")

	tungguHingga(t, func() bool {
		return r.StatusPerangkat() == tcpctl.StatusOnline.String()
	}, "controller tersambung")

	h := r.Health(time.Second)
	if h.Status != HealthDegraded {
		t.Fatalf("status = %q, want %q (%+v)", h.Status, HealthDegraded, h)
	}
	if len(h.Disimulasikan) == 0 {
		t.Fatalf("printer tersimulasi harus terdaftar: %+v", h)
	}
	if len(h.Alasan) == 0 {
		t.Fatalf("degraded harus menyebutkan alasannya")
	}

	// Rollup lahan ikut degraded — satu gerbang tak utuh membuat lahan tak utuh.
	if kes := svc.Health(time.Second); kes.Status != HealthDegraded {
		t.Fatalf("rollup = %q, want %q", kes.Status, HealthDegraded)
	}
}

// Healthcheck internal mengumumkan sampel pertama, lalu diam selama status tak berubah.
//
// Lahan sepi menghasilkan sampel identik setiap beberapa detik; memancarkan semuanya akan
// mengubur event yang berarti di bawah aliran tetap tanpa kabar.
func TestHealthcheckMengumumkanPerubahanSaja(t *testing.T) {
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site:               gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source:             duaMasukSatuKeluar(),
		HealthInterval:     20 * time.Millisecond,
		HealthProbeTimeout: 500 * time.Millisecond,
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Berlangganan SEBELUM Start supaya sampel pertama tak terlewat.
	ch, unsub := hub.Subscribe()
	defer unsub()
	svc.Start()
	t.Cleanup(svc.Close)

	terlihat := map[string]int{}
	tenggat := time.After(time.Second)
	for len(terlihat) < 3 {
		select {
		case ev := <-ch:
			if ev.Name != "gate.health.changed" {
				continue
			}
			code, _ := ev.Data["gate_code"].(string)
			terlihat[code]++
			if got := ev.Data["status"]; got != HealthOK {
				t.Fatalf("status %s = %v, want %q", code, got, HealthOK)
			}
		case <-tenggat:
			t.Fatalf("sampel pertama tak diumumkan untuk semua gerbang: %v", terlihat)
		}
	}

	// Beberapa tick berlalu tanpa perubahan status — tak boleh ada pengumuman susulan.
	diam := time.After(200 * time.Millisecond)
	for {
		select {
		case ev := <-ch:
			if ev.Name == "gate.health.changed" {
				t.Fatalf("status tak berubah tapi diumumkan lagi: %v", ev.Data)
			}
		case <-diam:
			for code, n := range terlihat {
				if n != 1 {
					t.Fatalf("%s diumumkan %d kali, want 1", code, n)
				}
			}
			return
		}
	}
}

// Watchdog service (task 3.1) bertanya "apakah mesin internal masih berjalan", bukan
// "apakah gerbangnya sehat". Bedanya menentukan apakah proses dibunuh atau tidak.
func TestMesinInternalHidup(t *testing.T) {
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})

	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site:               gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
		Source:             duaMasukSatuKeluar(),
		HealthInterval:     20 * time.Millisecond,
		HealthProbeTimeout: 200 * time.Millisecond,
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Sebelum Start belum ada sapuan sama sekali. Menyatakannya mati akan membuat
	// watchdog membunuh proses tepat saat ia sedang bangun.
	if !svc.MesinInternalHidup() {
		t.Fatal("Service yang belum Start tak boleh dinyatakan beku")
	}
	if !svc.LastHealthSweep().IsZero() {
		t.Fatal("LastHealthSweep harus nol sebelum Start")
	}

	svc.Start()
	tungguSapuan(t, svc)

	if !svc.MesinInternalHidup() {
		t.Fatalf("mesin internal dinyatakan beku padahal menyapu: %v", svc.LastHealthSweep())
	}

	// Gelang berhenti (di lapangan: goroutine-nya membeku) → watchdog harus menahan ping.
	svc.Close()
	batas := time.Now().Add(2 * time.Second)
	for time.Now().Before(batas) {
		if !svc.MesinInternalHidup() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("gelang berhenti menyapu tapi mesin masih dinyatakan hidup (sapuan terakhir %v)",
		svc.LastHealthSweep())
}

// Gerbang yang mati TIDAK boleh membuat mesin internal dinyatakan beku — restart tak
// menyambungkan kabel, dan ia menjatuhkan gerbang lain yang masih melayani (P8, K26).
func TestGerbangMatiTakMembekukanMesinInternal(t *testing.T) {
	src := StaticSource{
		{Code: "GATE-IN-01", Kind: KindEntry, Transport: TransportTCP, Endpoint: "127.0.0.1:1"},
		{Code: "GATE-OUT-01", Kind: KindExit, Transport: TransportSim},
	}
	svc, _ := svcKesehatan(t, src, 20*time.Millisecond, 200*time.Millisecond)
	tungguSapuan(t, svc)

	if kes := svc.Health(time.Second); kes.Status != HealthDown {
		t.Fatalf("prasyarat gugur: rollup = %q, want %q", kes.Status, HealthDown)
	}
	if !svc.MesinInternalHidup() {
		t.Fatal("gerbang mati membekukan penilaian mesin internal — proses akan direstart sia-sia")
	}
}

func tungguSapuan(t *testing.T, svc *Service) {
	t.Helper()
	batas := time.Now().Add(3 * time.Second)
	for time.Now().Before(batas) {
		if !svc.LastHealthSweep().IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timeout menunggu sapuan healthcheck pertama")
}
