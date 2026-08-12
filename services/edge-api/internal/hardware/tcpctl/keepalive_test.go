package tcpctl

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// penjawab membuat perangkat palsu membalas PING dengan PINGOK, dan bisa dibuat bisu
// di tengah jalan untuk meniru controller yang hang tanpa memutus socket — persis
// kegagalan yang bikin keepalive aplikasi ada.
type penjawab struct {
	pings atomic.Int64
	bisu  atomic.Bool
}

func mulaiPenjawab(conn net.Conn) *penjawab {
	r := &penjawab{}
	go func() {
		p := NewParser()
		buf := make([]byte, 256)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				p.Feed(buf[:n])
				for {
					cmd, ok := p.Next()
					if !ok {
						break
					}
					if cmd != CmdPing {
						continue
					}
					r.pings.Add(1)
					if !r.bisu.Load() {
						if _, err := conn.Write(frame(RespPingOK)); err != nil {
							return
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

// diamkan menghentikan balasan PINGOK; socket sengaja dibiarkan terbuka.
func (r *penjawab) diamkan() { r.bisu.Store(true) }

// opsiKeepaliveUji: PING cepat supaya tes selesai dalam milidetik, bukan detik.
func opsiKeepaliveUji() []DeviceOption {
	return []DeviceOption{
		WithReconnectBackoff(backoffUji()),
		WithPingInterval(20 * time.Millisecond),
		WithMaxMissedPing(3),
	}
}

func TestKeepaliveMengirimPingBerkala(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), opsiKeepaliveUji()...)
	conn := p.terima(t)
	r := mulaiPenjawab(conn)

	tungguSampai(t, 3*time.Second, func() bool {
		return r.pings.Load() >= 3
	}, "beberapa PING terkirim")

	if got := d.Stats().PingsSent; got < 3 {
		t.Fatalf("PingsSent = %d, want >= 3", got)
	}
}

// Controller yang membalas PING harus tetap ONLINE selamanya.
func TestKeepalivePingokMenjagaStatusOnline(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), opsiKeepaliveUji()...)
	conn := p.terima(t)
	r := mulaiPenjawab(conn)

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().Pongs >= 5
	}, "beberapa PINGOK diterima")

	// Jauh melewati ambang 3× interval — kalau pelacakan salah, status sudah jatuh.
	time.Sleep(150 * time.Millisecond)

	if st := d.Status(); st != StatusOnline {
		t.Fatalf("Status = %v, want ONLINE", st)
	}
	if got := d.Stats().Unresponsive; got != 0 {
		t.Fatalf("Unresponsive = %d, want 0 selama controller membalas", got)
	}
	if r.pings.Load() == 0 {
		t.Fatal("perangkat tidak menerima PING sama sekali")
	}
}

// PINGOK adalah housekeeping 1 Hz; meneruskannya ke state machine cuma derau.
func TestKeepalivePingokTidakDiteruskanKeFrames(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), opsiKeepaliveUji()...)
	conn := p.terima(t)
	r := mulaiPenjawab(conn)

	// Biarkan beberapa siklus PING/PINGOK lewat dulu.
	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().Pongs >= 3
	}, "beberapa PINGOK diterima")
	_ = r

	if _, err := conn.Write(frame("IN1ON")); err != nil {
		t.Fatalf("perangkat gagal menulis event: %v", err)
	}

	// Frame pertama yang sampai ke konsumen harus event nyata, bukan PINGOK.
	if got := tungguFrameDevice(t, d); got != "IN1ON" {
		t.Fatalf("frame = %q, want IN1ON (PINGOK seharusnya tidak diteruskan)", got)
	}
}

// Inti task 1.3: controller bisu → OFFLINE setelah 3× PING tanpa balasan, koneksi
// diputus paksa, lalu supervisor menyambung ulang.
func TestDeviceJadiUnresponsiveSaatPingTakDibalas(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), opsiKeepaliveUji()...)
	conn1 := p.terima(t)
	r := mulaiPenjawab(conn1)

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().Pongs >= 2
	}, "controller sempat sehat")
	if st := d.Status(); st != StatusOnline {
		t.Fatalf("Status awal = %v, want ONLINE", st)
	}

	// Controller hang: socket tetap terbuka, tapi berhenti membalas.
	r.diamkan()

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().Unresponsive >= 1
	}, "controller ditandai bisu")

	// Koneksi harus diputus paksa sehingga supervisor men-dial ulang.
	conn2 := p.terima(t)
	if conn2 == nil {
		t.Fatal("tidak ada koneksi ulang setelah controller bisu")
	}
	if got := d.Stats().Disconnects; got < 1 {
		t.Fatalf("Disconnects = %d, want >= 1", got)
	}
}

// Ambang harus benar-benar dihormati: dengan maxMissed=3, dua PING tak dibalas belum
// boleh menjatuhkan koneksi yang sehat.
func TestDeviceBertahanDiBawahAmbangPingGagal(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(),
		WithReconnectBackoff(backoffUji()),
		WithPingInterval(30*time.Millisecond),
		WithMaxMissedPing(50), // ambang sangat tinggi
	)
	conn := p.terima(t)
	mulaiPenjawab(conn)

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().PingsSent >= 5
	}, "beberapa PING terkirim")

	if got := d.Stats().Unresponsive; got != 0 {
		t.Fatalf("Unresponsive = %d, want 0 di bawah ambang", got)
	}
	if st := d.Status(); st != StatusOnline {
		t.Fatalf("Status = %v, want ONLINE", st)
	}
}

func TestStatusChangesMengalirkanTransisi(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), opsiKeepaliveUji()...)
	conn1 := p.terima(t)
	r := mulaiPenjawab(conn1)

	// Transisi pertama: DISCONNECTED → ONLINE.
	select {
	case st, ok := <-d.StatusChanges():
		if !ok {
			t.Fatal("kanal StatusChanges tertutup tak terduga")
		}
		if st != StatusOnline {
			t.Fatalf("transisi pertama = %v, want ONLINE", st)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu transisi ONLINE")
	}

	r.diamkan()

	// Berikutnya harus melewati UNRESPONSIVE sebelum kembali DISCONNECTED.
	lihatUnresponsive := false
	tenggat := time.After(3 * time.Second)
	for !lihatUnresponsive {
		select {
		case st, ok := <-d.StatusChanges():
			if !ok {
				t.Fatal("kanal StatusChanges tertutup tak terduga")
			}
			if st == StatusUnresponsive {
				lihatUnresponsive = true
			}
		case <-tenggat:
			t.Fatal("timeout menunggu transisi UNRESPONSIVE")
		}
	}
}

func TestStatusChangesTertutupSaatDeviceBerhenti(t *testing.T) {
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
		case _, ok := <-d.StatusChanges():
			if !ok {
				return // tertutup sebagaimana mestinya
			}
		case <-tenggat:
			t.Fatal("kanal StatusChanges tidak tertutup setelah Close")
		}
	}
}

// Katup darurat bila controller lapangan ternyata tak mematuhi kontrak PING (§5.5/§13).
func TestKeepaliveDapatDimatikan(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), WithPingInterval(0))
	conn := p.terima(t)
	r := mulaiPenjawab(conn)

	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")
	time.Sleep(150 * time.Millisecond)

	if got := r.pings.Load(); got != 0 {
		t.Fatalf("perangkat menerima %d PING, want 0 saat keepalive dimatikan", got)
	}
	if got := d.Stats().Unresponsive; got != 0 {
		t.Fatalf("Unresponsive = %d, want 0 saat keepalive dimatikan", got)
	}
	if st := d.Status(); st != StatusOnline {
		t.Fatalf("Status = %v, want ONLINE", st)
	}
}

func TestOpsiKeepaliveMenolakNilaiTakSah(t *testing.T) {
	d := NewDevice("127.0.0.1:1", WithMaxMissedPing(0))
	if d.maxMissedPing != DefaultMaxMissedPing {
		t.Fatalf("maxMissedPing = %d, want bawaan %d", d.maxMissedPing, DefaultMaxMissedPing)
	}
	if def := NewDevice("127.0.0.1:1"); def.pingInterval != DefaultPingInterval {
		t.Fatalf("pingInterval bawaan = %v, want %v", def.pingInterval, DefaultPingInterval)
	}
}

func TestDeviceStatusString(t *testing.T) {
	cases := []struct {
		st     DeviceStatus
		nama   string
		online bool
	}{
		{StatusDisconnected, "DISCONNECTED", false},
		{StatusOnline, "ONLINE", true},
		{StatusUnresponsive, "UNRESPONSIVE", false},
	}
	for _, tc := range cases {
		if got := tc.st.String(); got != tc.nama {
			t.Fatalf("String() = %q, want %q", got, tc.nama)
		}
		if got := tc.st.Online(); got != tc.online {
			t.Fatalf("%s.Online() = %v, want %v", tc.nama, got, tc.online)
		}
	}
}
