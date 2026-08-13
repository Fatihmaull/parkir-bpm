package svcnotify

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// manajerPalsu membuka soket unixgram seperti systemd, lalu memasang NOTIFY_SOCKET.
func manajerPalsu(t *testing.T) *net.UnixConn {
	t.Helper()

	// Soket unix punya batas panjang path (~104 byte); t.TempDir() bisa panjang, jadi
	// dipakai direktori sementara yang pendek.
	dir, err := os.MkdirTemp("", "sn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "n.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	t.Setenv("NOTIFY_SOCKET", path)
	return conn
}

func baca(t *testing.T, conn *net.UnixConn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("baca pesan: %v", err)
	}
	return string(buf[:n])
}

// Tanpa NOTIFY_SOCKET, seluruh operasi harus menjadi tanpa-operasi yang BERHASIL.
//
// Ini bukan kenyamanan: edge-api juga berjalan di Windows, di dev lokal, dan di kontainer
// tanpa systemd. Kalau ketiadaan systemd menjadi error, jalur startup harus bercabang di
// banyak tempat — dan cabang yang jarang dijalankan adalah cabang yang membusuk.
func TestTanpaSystemdMenjadiTanpaOperasi(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	n, err := New()
	if err != nil {
		t.Fatalf("New tanpa NOTIFY_SOCKET: %v", err)
	}
	if n.Aktif() {
		t.Fatal("Aktif() harus false tanpa NOTIFY_SOCKET")
	}
	// Nil-Notifier harus aman disentuh tanpa penjagaan di pemanggil.
	for nama, f := range map[string]func() error{
		"Ready": n.Ready, "Watchdog": n.Watchdog, "Stopping": n.Stopping, "Close": n.Close,
	} {
		if err := f(); err != nil {
			t.Fatalf("%s pada Notifier nil: %v", nama, err)
		}
	}
	if err := n.Status("apa pun"); err != nil {
		t.Fatalf("Status pada Notifier nil: %v", err)
	}
	if got := n.WatchdogInterval(); got != 0 {
		t.Fatalf("WatchdogInterval = %v, want 0", got)
	}
}

func TestPesanTerkirimKeManajer(t *testing.T) {
	conn := manajerPalsu(t)

	n, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	if !n.Aktif() {
		t.Fatal("Aktif() harus true saat NOTIFY_SOCKET ada")
	}

	for _, tc := range []struct {
		nama  string
		kirim func() error
		want  string
	}{
		{"Ready", n.Ready, "READY=1"},
		{"Watchdog", n.Watchdog, "WATCHDOG=1"},
		{"Stopping", n.Stopping, "STOPPING=1"},
	} {
		if err := tc.kirim(); err != nil {
			t.Fatalf("%s: %v", tc.nama, err)
		}
		if got := baca(t, conn); got != tc.want {
			t.Fatalf("%s mengirim %q, want %q", tc.nama, got, tc.want)
		}
	}
}

// Newline pada STATUS harus dibuang: protokol sd_notify memisahkan perintah dengan
// newline, jadi keterangan yang memuatnya bisa berubah menjadi perintah kedua yang tak
// pernah dimaksudkan.
func TestStatusTakBisaMenyuntikPerintah(t *testing.T) {
	conn := manajerPalsu(t)

	n, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	if err := n.Status("gerbang siap\nREADY=1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	got := baca(t, conn)
	if got != "STATUS=gerbang siap READY=1" {
		t.Fatalf("Status mengirim %q — newline tak dibersihkan", got)
	}
}

// systemd menaruh BATAS WAKTU di WATCHDOG_USEC; ping harus dikirim pada setengahnya.
// Mengirim tepat pada batas berarti keterlambatan sekecil apa pun dibaca sebagai proses
// beku, dan gerbang direstart tanpa ada yang rusak.
func TestIntervalWatchdogSetengahDariBatas(t *testing.T) {
	manajerPalsu(t)
	t.Setenv("WATCHDOG_USEC", "30000000") // 30 dtk

	n, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	if got := n.WatchdogInterval(); got != 15*time.Second {
		t.Fatalf("WatchdogInterval = %v, want 15s (setengah dari 30s)", got)
	}
}

// WATCHDOG_PID milik proses lain berarti watchdog itu bukan untuk kita.
func TestWatchdogDiabaikanBilaPIDBukanMilikKita(t *testing.T) {
	manajerPalsu(t)
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()+1))

	n, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	if got := n.WatchdogInterval(); got != 0 {
		t.Fatalf("WatchdogInterval = %v, want 0 — watchdog milik proses lain", got)
	}
}

func TestWatchdogTakDimintaSaatUsecKosong(t *testing.T) {
	manajerPalsu(t)
	t.Setenv("WATCHDOG_USEC", "")

	n, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })

	if got := n.WatchdogInterval(); got != 0 {
		t.Fatalf("WatchdogInterval = %v, want 0", got)
	}
}

// NOTIFY_SOCKET yang tak dapat disambung adalah kesalahan konfigurasi service, bukan
// keadaan normal — ia harus dilaporkan, bukan ditelan menjadi mode tanpa-operasi.
func TestSoketTakTerjangkauMenjadiError(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "/tak/ada/direktori/n.sock")

	if _, err := New(); err == nil {
		t.Fatal("New harus gagal saat NOTIFY_SOCKET menunjuk soket yang tak ada")
	}
}
