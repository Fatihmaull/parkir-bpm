// Package svcnotify bicara protokol sd_notify(3) milik systemd, supaya edge-api dapat
// dijalankan sebagai service yang diawasi manajer service (task 3.1, PRD v3 §12.x).
//
// Kenapa perlu, padahal Restart=always sudah ada: Restart=always hanya menangkap proses
// yang MATI. Proses yang hidup tetapi membeku — goroutine terkunci, deadlock, disk
// menggantung — tetap dihitung "aktif" oleh systemd, dan gerbang berhenti melayani tanpa
// ada yang menyadarinya. Watchdog menutup celah itu: proses harus membuktikan dirinya
// masih berjalan secara berkala, dan yang gagal membuktikan akan dibunuh lalu dinyalakan
// ulang.
//
// Paket ini murni Go, tanpa cgo dan tanpa libsystemd. Di luar systemd (Windows, dev
// lokal, kontainer tanpa systemd) NOTIFY_SOCKET tidak ada dan seluruh operasi menjadi
// tanpa-operasi yang berhasil — pemanggil tidak perlu bercabang.
package svcnotify

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Notifier mengirim pesan status ke manajer service.
//
// Nil-Notifier aman dipakai: seluruh metodenya menjadi tanpa-operasi. Dengan begitu
// jalur "tidak di bawah systemd" tidak butuh cabang khusus di pemanggil.
type Notifier struct {
	conn     *net.UnixConn
	interval time.Duration
}

// New menyambung ke NOTIFY_SOCKET bila ada.
//
// Mengembalikan (nil, nil) bila proses tidak berjalan di bawah manajer service yang
// memintanya — itu keadaan normal, bukan kesalahan.
func New() (*Notifier, error) {
	alamat := os.Getenv("NOTIFY_SOCKET")
	if alamat == "" {
		return nil, nil
	}

	// Alamat berawalan '@' berarti abstract socket namespace Linux; systemd
	// menuliskannya begitu karena NUL tak dapat dibawa lewat variabel lingkungan.
	if strings.HasPrefix(alamat, "@") {
		alamat = "\x00" + alamat[1:]
	}

	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: alamat, Net: "unixgram"})
	if err != nil {
		return nil, fmt.Errorf("svcnotify: gagal menyambung ke NOTIFY_SOCKET: %w", err)
	}

	return &Notifier{conn: conn, interval: intervalWatchdog()}, nil
}

// intervalWatchdog membaca WATCHDOG_USEC dan mengembalikan JEDA PING yang dianjurkan.
//
// systemd menaruh BATAS WAKTU di WATCHDOG_USEC, bukan jeda ping. Mengirim ping tepat
// pada batas itu berarti setiap keterlambatan sekecil apa pun — GC, disk lambat, mesin
// sibuk — dibaca sebagai proses beku dan gerbang direstart tanpa ada yang rusak. Ping
// dikirim pada setengah batas, sesuai anjuran sd_watchdog_enabled(3).
//
// WATCHDOG_PID ikut diperiksa: bila ada dan bukan PID kita, watchdog itu ditujukan untuk
// proses lain (mis. kita hasil fork) dan tak boleh kita jawab.
func intervalWatchdog() time.Duration {
	raw := os.Getenv("WATCHDOG_USEC")
	if raw == "" {
		return 0
	}
	usec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || usec <= 0 {
		return 0
	}
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" {
		if p, err := strconv.Atoi(pid); err != nil || p != os.Getpid() {
			return 0
		}
	}
	return time.Duration(usec) * time.Microsecond / 2
}

// Aktif melaporkan apakah ada manajer service yang mendengarkan.
func (n *Notifier) Aktif() bool { return n != nil && n.conn != nil }

// WatchdogInterval mengembalikan jeda ping yang dianjurkan, atau 0 bila manajer service
// tidak meminta watchdog.
func (n *Notifier) WatchdogInterval() time.Duration {
	if !n.Aktif() {
		return 0
	}
	return n.interval
}

// Ready mengumumkan proses selesai memulai dan siap melayani.
//
// Ini yang membuat `Type=notify` berguna: systemd menahan unit dependen sampai pesan ini
// tiba, bukan sekadar sampai proses ter-fork. Untuk edge-api artinya "gerbang sudah
// dirangkai dan HTTP sudah mendengarkan" — bukan "biner sudah dieksekusi".
func (n *Notifier) Ready() error { return n.kirim("READY=1") }

// Watchdog membuktikan proses masih berjalan.
func (n *Notifier) Watchdog() error { return n.kirim("WATCHDOG=1") }

// Stopping mengumumkan proses sedang berhenti dengan sengaja, supaya manajer service
// tidak membacanya sebagai kematian mendadak.
func (n *Notifier) Stopping() error { return n.kirim("STOPPING=1") }

// Status mengirim satu baris keterangan yang muncul di `systemctl status`.
func (n *Notifier) Status(s string) error {
	// Newline akan memecah pesan menjadi dua perintah protokol; buang agar keterangan
	// tak pernah berubah jadi arahan yang tak disengaja.
	return n.kirim("STATUS=" + strings.ReplaceAll(s, "\n", " "))
}

// Close menutup soket.
func (n *Notifier) Close() error {
	if !n.Aktif() {
		return nil
	}
	return n.conn.Close()
}

func (n *Notifier) kirim(pesan string) error {
	if !n.Aktif() {
		return nil
	}
	if _, err := n.conn.Write([]byte(pesan)); err != nil {
		return fmt.Errorf("svcnotify: gagal mengirim %q: %w", pesan, err)
	}
	return nil
}
