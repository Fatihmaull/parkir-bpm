package tcpctl

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// backoffUji mempercepat deret agar tes reconnect selesai dalam milidetik.
func backoffUji() Backoff {
	return Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond, Factor: 2, Jitter: tanpaJitter}
}

// opsiDasarUji: backoff cepat, dan keepalive aplikasi dimatikan supaya tes siklus
// koneksi (task 1.2) tidak tercampur PING otomatis. Tes keepalive sendiri ada di
// keepalive_test.go dan menyalakannya kembali secara eksplisit.
// opsiDasarUji mematikan seluruh lalu lintas latar yang dikirim Device atas
// inisiatifnya sendiri — keepalive PING dan resync STAT (task 3.2). Keduanya menyalakan
// perintah yang tak diminta oleh uji, sehingga akan mengacaukan hitungan Exec dan
// menyuntikkan LoopEvent dari potret STAT bawaan controllerPalsu (STAT10001000).
//
// Resync punya uji tersendiri: TestSeed* di resync_test.go dan TestIntegrasiResync*
// terhadap simdev.
func opsiDasarUji() []DeviceOption {
	return []DeviceOption{
		WithReconnectBackoff(backoffUji()),
		WithPingInterval(0),
		WithStatResync(false),
	}
}

func mulaiDevice(t *testing.T, addr string, opts ...DeviceOption) *Device {
	t.Helper()
	semua := append(opsiDasarUji(), opts...)
	d := NewDevice(addr, semua...)

	ctx, batal := context.WithCancel(context.Background())
	t.Cleanup(batal)
	d.Start(ctx)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func tungguFrameDevice(t *testing.T, d *Device) string {
	t.Helper()
	select {
	case got, ok := <-d.Frames():
		if !ok {
			t.Fatal("kanal Frames Device tertutup tak terduga")
		}
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu frame dari Device")
		return ""
	}
}

func tungguSampai(t *testing.T, batas time.Duration, cek func() bool, pesan string) {
	t.Helper()
	tenggat := time.Now().Add(batas)
	for time.Now().Before(tenggat) {
		if cek() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout menunggu: %s", pesan)
}

// alamatMati mengembalikan alamat yang dijamin tidak ada yang mendengarkan.
func alamatMati(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

func TestDeviceMenyambungDanMenerimaFrame(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())
	conn := p.terima(t)

	if _, err := conn.Write(frame("IN1ON")); err != nil {
		t.Fatalf("perangkat gagal menulis: %v", err)
	}
	if got := tungguFrameDevice(t, d); got != "IN1ON" {
		t.Fatalf("frame = %q, want IN1ON", got)
	}
	if !d.Connected() {
		t.Fatal("Connected() = false, want true")
	}
}

// Inti task 1.2: perangkat putus lalu tersambung lagi sendiri, dan kanal Frames()
// TIDAK tertutup — state machine gerbang di atasnya tak perlu tahu ada gangguan.
func TestDeviceMenyambungUlangSetelahPerangkatPutus(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())

	conn1 := p.terima(t)
	if _, err := conn1.Write(frame("IN1ON")); err != nil {
		t.Fatalf("sesi 1 gagal menulis: %v", err)
	}
	if got := tungguFrameDevice(t, d); got != "IN1ON" {
		t.Fatalf("sesi 1: frame = %q, want IN1ON", got)
	}

	// Kabel LAN dicabut / controller reboot.
	if err := conn1.Close(); err != nil {
		t.Fatalf("gagal memutus sesi 1: %v", err)
	}

	// Supervisor harus men-dial ulang tanpa campur tangan siapa pun.
	conn2 := p.terima(t)
	if _, err := conn2.Write(frame("IN2ON")); err != nil {
		t.Fatalf("sesi 2 gagal menulis: %v", err)
	}
	if got := tungguFrameDevice(t, d); got != "IN2ON" {
		t.Fatalf("sesi 2: frame = %q, want IN2ON", got)
	}

	st := d.Stats()
	if st.Connects < 2 {
		t.Fatalf("Connects = %d, want >= 2", st.Connects)
	}
	if st.Disconnects < 1 {
		t.Fatalf("Disconnects = %d, want >= 1", st.Disconnects)
	}
}

// Perintah setelah pulih harus benar-benar sampai ke socket yang baru.
func TestDeviceKirimLewatKoneksiBaruSetelahPulih(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())

	conn1 := p.terima(t)
	if err := conn1.Close(); err != nil {
		t.Fatalf("gagal memutus sesi 1: %v", err)
	}

	conn2 := p.terima(t)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung kembali")

	if err := d.Send(context.Background(), CmdPing); err != nil {
		t.Fatalf("Send setelah pulih: %v", err)
	}

	want := frame(CmdPing)
	got := make([]byte, len(want))
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn2, got); err != nil {
		t.Fatalf("sesi 2 gagal membaca: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("byte di kabel = % X, want % X", got, want)
	}
}

// Perintah tidak boleh diantre diam-diam saat controller terputus: `OUT1OFF` yang
// tertunda bisa menutup palang di atas kendaraan lain (P4).
func TestDeviceSendSaatTerputusDitolak(t *testing.T) {
	d := mulaiDevice(t, alamatMati(t))

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().DialFailures > 0
	}, "dial gagal tercatat")

	if d.Connected() {
		t.Fatal("Connected() = true padahal alamat mati")
	}
	if err := d.Send(context.Background(), CmdPing); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Send saat terputus = %v, want ErrNotConnected", err)
	}
}

// Controller yang tak pernah muncul tidak boleh membuat supervisor menyerah —
// lahan harus tetap mencoba sampai teknisi memperbaikinya (P8).
func TestDeviceTerusMencobaSaatAlamatMati(t *testing.T) {
	d := mulaiDevice(t, alamatMati(t))

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().DialFailures >= 3
	}, "beberapa percobaan dial")

	select {
	case _, ok := <-d.Frames():
		if !ok {
			t.Fatal("kanal Frames tertutup padahal supervisor harus terus mencoba")
		}
	default:
	}
}

func TestDeviceCloseMenutupKanalDanMenghentikanSupervisor(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := NewDevice(p.alamat(), opsiDasarUji()...)
	d.Start(context.Background())
	p.terima(t)

	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-d.Frames():
		if ok {
			t.Fatal("kanal Frames masih mengirim setelah Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kanal Frames tidak tertutup setelah Close")
	}
	if err := d.Send(context.Background(), CmdPing); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send setelah Close = %v, want ErrClosed", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close kedua: %v", err)
	}
}

func TestDeviceCloseTanpaStart(t *testing.T) {
	d := NewDevice(alamatMati(t))
	if err := d.Close(); err != nil {
		t.Fatalf("Close tanpa Start: %v", err)
	}
	select {
	case _, ok := <-d.Frames():
		if ok {
			t.Fatal("kanal Frames tidak boleh mengirim")
		}
	case <-time.After(time.Second):
		t.Fatal("kanal Frames tidak tertutup")
	}
}

func TestDevicePembatalanCtxMenghentikanSupervisor(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := NewDevice(p.alamat(), opsiDasarUji()...)
	t.Cleanup(func() { _ = d.Close() })

	ctx, batal := context.WithCancel(context.Background())
	d.Start(ctx)
	p.terima(t)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")

	batal()
	select {
	case _, ok := <-d.Frames():
		if ok {
			t.Fatal("kanal Frames masih mengirim setelah ctx dibatalkan")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kanal Frames tidak tertutup setelah ctx dibatalkan")
	}
}

func TestDeviceStartKeduaTidakBerefek(t *testing.T) {
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat())
	p.terima(t)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")

	// Pemanggilan kedua tidak boleh memunculkan supervisor tambahan.
	d.Start(context.Background())

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpsiKeepAliveTersalurKeKlien(t *testing.T) {
	c := &Client{}
	WithKeepAlive(30 * time.Second)(c)
	if c.keepAlive != 30*time.Second {
		t.Fatalf("keepAlive = %v, want 30s", c.keepAlive)
	}

	d := NewDevice("127.0.0.1:1", WithDeviceKeepAlive(45*time.Second))
	if d.keepAlive != 45*time.Second {
		t.Fatalf("Device.keepAlive = %v, want 45s", d.keepAlive)
	}

	// Bawaan harus aktif, bukan nol (nol = keep-alive socket mati).
	if def := NewDevice("127.0.0.1:1"); def.keepAlive != defaultKeepAlive {
		t.Fatalf("keepAlive bawaan = %v, want %v", def.keepAlive, defaultKeepAlive)
	}
}
