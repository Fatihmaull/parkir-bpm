package tcpctl

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// perangkatPalsu adalah server TCP A6/A9 minimal untuk menguji klien tanpa controller
// fisik (P7 — semua perangkat punya simulator). Ini perkakas uji seperlunya; simulator
// device lengkap yang dipakai uji integrasi CI adalah task 1.10.
type perangkatPalsu struct {
	ln     net.Listener
	connCh chan net.Conn
}

func mulaiPerangkatPalsu(t *testing.T) *perangkatPalsu {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &perangkatPalsu{ln: ln, connCh: make(chan net.Conn, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		d.connCh <- conn
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return d
}

func (d *perangkatPalsu) alamat() string { return d.ln.Addr().String() }

// terima menunggu koneksi dari Edge dan mengembalikannya.
func (d *perangkatPalsu) terima(t *testing.T) net.Conn {
	t.Helper()
	select {
	case c := <-d.connCh:
		t.Cleanup(func() { _ = c.Close() })
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timeout menunggu koneksi masuk")
		return nil
	}
}

// dialUji membuka Client ke perangkat palsu dan memastikan ia ditutup di akhir tes.
func dialUji(t *testing.T, addr string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// tungguFrame menunggu satu frame dari klien dengan batas waktu.
func tungguFrame(t *testing.T, c *Client) string {
	t.Helper()
	select {
	case got, ok := <-c.Frames():
		if !ok {
			t.Fatal("kanal Frames tertutup tak terduga")
		}
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timeout menunggu frame")
		return ""
	}
}

// tungguKanalTertutup memastikan Frames() tertutup dalam batas waktu.
func tungguKanalTertutup(t *testing.T, c *Client) {
	t.Helper()
	for {
		select {
		case _, ok := <-c.Frames():
			if !ok {
				return
			}
			// Abaikan frame sisa yang sempat terbaca sebelum koneksi berakhir.
		case <-time.After(2 * time.Second):
			t.Fatal("timeout menunggu kanal Frames tertutup")
		}
	}
}

func TestClientKirimFrameKeKabel(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	conn := d.terima(t)

	cmd, err := OutOn(1)
	if err != nil {
		t.Fatalf("OutOn: %v", err)
	}
	if err := c.Send(context.Background(), cmd); err != nil {
		t.Fatalf("Send: %v", err)
	}

	want := frame("OUT1ON")
	got := make([]byte, len(want))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("perangkat gagal membaca: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("byte di kabel = % X, want % X", got, want)
	}
}

func TestClientMenolakPerintahTakSahSebelumMenyentuhSocket(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	d.terima(t)

	if err := c.Send(context.Background(), ""); !errors.Is(err, ErrEmptyCommand) {
		t.Fatalf("Send(\"\") error = %v, want ErrEmptyCommand", err)
	}
	if err := c.Send(context.Background(), "OUT1\xA6ON"); !errors.Is(err, ErrNonASCII) {
		t.Fatalf("Send(non-ASCII) error = %v, want ErrNonASCII", err)
	}
}

func TestClientTerimaEventTakDiminta(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	conn := d.terima(t)

	// Urutan realistis satu kendaraan masuk: LD1 naik, LD2 naik, lalu tap member.
	for _, evt := range []string{"IN1ON", "IN2ON", "W1A2B3C"} {
		if _, err := conn.Write(frame(evt)); err != nil {
			t.Fatalf("perangkat gagal menulis %s: %v", evt, err)
		}
	}

	for _, want := range []string{"IN1ON", "IN2ON", "W1A2B3C"} {
		if got := tungguFrame(t, c); got != want {
			t.Fatalf("frame = %q, want %q", got, want)
		}
	}
}

// TCP boleh memecah frame sesukanya; klien harus tetap menyatukannya.
func TestClientFrameTerpecahDiKabel(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	conn := d.terima(t)

	potongan := [][]byte{{Header}, []byte("STAT"), []byte("11000100"), {Footer}}
	for _, p := range potongan {
		if _, err := conn.Write(p); err != nil {
			t.Fatalf("perangkat gagal menulis: %v", err)
		}
		time.Sleep(5 * time.Millisecond) // paksa tiba di paket terpisah
	}

	if got := tungguFrame(t, c); got != "STAT11000100" {
		t.Fatalf("frame = %q, want STAT11000100", got)
	}
}

// Derau di saluran tidak boleh menjatuhkan koneksi — frame sah sesudahnya tetap lolos.
func TestClientBertahanTerhadapDerau(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	conn := d.terima(t)

	if _, err := conn.Write([]byte{0x00, 0xFF, 0x7F}); err != nil {
		t.Fatalf("perangkat gagal menulis derau: %v", err)
	}
	if _, err := conn.Write(frame("PINGOK")); err != nil {
		t.Fatalf("perangkat gagal menulis frame: %v", err)
	}

	if got := tungguFrame(t, c); got != RespPingOK {
		t.Fatalf("frame = %q, want %q", got, RespPingOK)
	}
	if st := c.Stats(); st.JunkBytes != 3 || st.Frames != 1 {
		t.Fatalf("Stats = %+v, want JunkBytes=3 Frames=1", st)
	}
}

func TestClientCloseMenutupKanalTanpaError(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	d.terima(t)

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	tungguKanalTertutup(t, c)

	// Penutupan yang disengaja bukan kegagalan perangkat.
	if err := c.Err(); err != nil {
		t.Fatalf("Err setelah Close = %v, want nil", err)
	}
	// Close kedua kali harus aman.
	if err := c.Close(); err != nil {
		t.Fatalf("Close kedua: %v", err)
	}
}

func TestClientSendSetelahCloseDitolak(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	d.terima(t)

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Send(context.Background(), CmdPing); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send setelah Close = %v, want ErrClosed", err)
	}
}

// Controller mati / kabel LAN dicabut: kanal harus tertutup dan Err() terisi supaya
// lapisan atas dapat menandai device OFFLINE (task 1.3) dan men-dial ulang (task 1.2).
func TestClientDeteksiPutusnyaPerangkat(t *testing.T) {
	d := mulaiPerangkatPalsu(t)
	c := dialUji(t, d.alamat())
	conn := d.terima(t)

	if err := conn.Close(); err != nil {
		t.Fatalf("perangkat gagal menutup: %v", err)
	}
	tungguKanalTertutup(t, c)

	if err := c.Err(); err == nil {
		t.Fatal("Err setelah perangkat putus = nil, want error")
	}
}

func TestDialGagalKeAlamatMati(t *testing.T) {
	// Port yang baru saja ditutup — dial harus gagal, bukan menggantung.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	ctx, batal := context.WithTimeout(context.Background(), 2*time.Second)
	defer batal()
	if _, err := Dial(ctx, addr, WithDialTimeout(time.Second)); err == nil {
		t.Fatal("Dial ke alamat mati seharusnya gagal")
	}
}
