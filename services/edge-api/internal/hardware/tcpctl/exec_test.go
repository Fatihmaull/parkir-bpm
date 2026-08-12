package tcpctl

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// controllerPalsu membalas perintah sesuai kontrak §5.3, dan bisa disuruh membisu
// untuk sejumlah perintah berikutnya guna menguji retry.
type controllerPalsu struct {
	diterima chan string
	abaikan  atomic.Int32
}

func mulaiController(conn net.Conn) *controllerPalsu {
	c := &controllerPalsu{diterima: make(chan string, 64)}
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
					select {
					case c.diterima <- cmd:
					default:
					}
					if c.abaikan.Load() > 0 {
						c.abaikan.Add(-1)
						continue // sengaja tidak membalas
					}
					if _, err := conn.Write(frame(balasanUntuk(cmd))); err != nil {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

// balasanUntuk meniru §5.3: tiap perintah dibalas dirinya + "OK", kecuali STAT.
func balasanUntuk(cmd string) string {
	if cmd == CmdStat {
		return "STAT10001000"
	}
	return cmd + SuffixAck
}

func opsiExecUji() []DeviceOption {
	return []DeviceOption{
		WithReconnectBackoff(backoffUji()),
		WithPingInterval(0), // keepalive dimatikan agar tak mencampuri korelasi
		WithAckTimeout(50 * time.Millisecond),
		WithMaxAttempts(3),
	}
}

// siapkanExec menyiapkan Device + controller palsu yang sudah tersambung.
func siapkanExec(t *testing.T, opts ...DeviceOption) (*Device, *controllerPalsu, net.Conn) {
	t.Helper()
	p := mulaiPerangkatPalsu(t)
	d := mulaiDevice(t, p.alamat(), append(opsiExecUji(), opts...)...)
	conn := p.terima(t)
	c := mulaiController(conn)
	tungguSampai(t, 3*time.Second, d.Connected, "Device tersambung")
	return d, c, conn
}

func TestExecMenungguBalasan(t *testing.T) {
	d, _, _ := siapkanExec(t)

	cmd, err := OutOn(1)
	if err != nil {
		t.Fatalf("OutOn: %v", err)
	}
	ack, err := d.Exec(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if ack != "OUT1ONOK" {
		t.Fatalf("ack = %q, want OUT1ONOK", ack)
	}
	if got := d.Stats().Commands; got != 1 {
		t.Fatalf("Commands = %d, want 1", got)
	}
	if got := d.Stats().CommandRetries; got != 0 {
		t.Fatalf("CommandRetries = %d, want 0", got)
	}
}

// STAT dibalas STATabcdefgh, bukan STATOK (§5.3/§5.5) — pencocok harus tahu itu.
func TestExecStatDibalasPetaStatus(t *testing.T) {
	d, _, _ := siapkanExec(t)

	ack, err := d.Exec(context.Background(), CmdStat)
	if err != nil {
		t.Fatalf("Exec STAT: %v", err)
	}
	if ack != "STAT10001000" {
		t.Fatalf("ack = %q, want STAT10001000", ack)
	}
}

func TestExecMengulangSaatBalasanHilang(t *testing.T) {
	d, c, _ := siapkanExec(t)
	c.abaikan.Store(2) // dua perintah pertama dibiarkan tanpa balasan

	ack, err := d.Exec(context.Background(), CmdStat)
	if err != nil {
		t.Fatalf("Exec setelah retry: %v", err)
	}
	if ack != "STAT10001000" {
		t.Fatalf("ack = %q, want STAT10001000", ack)
	}

	st := d.Stats()
	if st.CommandRetries != 2 {
		t.Fatalf("CommandRetries = %d, want 2", st.CommandRetries)
	}
	if st.CommandTimeouts != 2 {
		t.Fatalf("CommandTimeouts = %d, want 2", st.CommandTimeouts)
	}
	if st.Commands != 1 {
		t.Fatalf("Commands = %d, want 1", st.Commands)
	}
}

func TestExecMenyerahSetelahBatasPercobaan(t *testing.T) {
	d, c, _ := siapkanExec(t)
	c.abaikan.Store(99) // controller bisu selamanya

	_, err := d.Exec(context.Background(), CmdStat)
	if !errors.Is(err, ErrAckTimeout) {
		t.Fatalf("Exec = %v, want terbungkus ErrAckTimeout", err)
	}

	st := d.Stats()
	if st.CommandTimeouts != 3 {
		t.Fatalf("CommandTimeouts = %d, want 3 (maxAttempts)", st.CommandTimeouts)
	}
	if st.Commands != 0 {
		t.Fatalf("Commands = %d, want 0", st.Commands)
	}

	// Tepat tiga kali dikirim ke kabel, tidak lebih.
	var terkirim int
	for {
		select {
		case <-c.diterima:
			terkirim++
			continue
		default:
		}
		break
	}
	if terkirim != 3 {
		t.Fatalf("perintah terkirim = %d, want 3", terkirim)
	}
}

// Terputus bukan alasan mengulang: mengirim ulang ke socket yang tak ada percuma,
// dan menahannya sampai pulih justru bahaya (lihat ErrNotConnected).
func TestExecTidakMengulangSaatTerputus(t *testing.T) {
	d := mulaiDevice(t, alamatMati(t), opsiExecUji()...)

	tungguSampai(t, 3*time.Second, func() bool {
		return d.Stats().DialFailures > 0
	}, "dial gagal tercatat")

	_, err := d.Exec(context.Background(), CmdStat)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Exec saat terputus = %v, want ErrNotConnected", err)
	}
	if got := d.Stats().CommandRetries; got != 0 {
		t.Fatalf("CommandRetries = %d, want 0", got)
	}
}

// Balasan yang sudah dikonsumsi Exec tidak boleh muncul lagi sebagai frame biasa.
func TestExecBalasanTidakDiteruskanKeFrames(t *testing.T) {
	d, _, conn := siapkanExec(t)

	if _, err := d.Exec(context.Background(), CmdStat); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, err := conn.Write(frame("IN1ON")); err != nil {
		t.Fatalf("perangkat gagal menulis event: %v", err)
	}

	if got := tungguFrameDevice(t, d); got != "IN1ON" {
		t.Fatalf("frame = %q, want IN1ON (balasan seharusnya sudah dikonsumsi)", got)
	}
}

// Event tak diminta harus tetap mengalir walau ada perintah sedang menunggu balasan —
// loop detector tidak boleh tertahan gara-gara palang sedang diperintah.
func TestExecEventTakDimintaTetapMengalirSaatMenunggu(t *testing.T) {
	d, c, conn := siapkanExec(t)
	c.abaikan.Store(1) // percobaan pertama tanpa balasan → ada jendela menunggu

	hasil := make(chan error, 1)
	go func() {
		_, err := d.Exec(context.Background(), CmdStat)
		hasil <- err
	}()

	if _, err := conn.Write(frame("IN2ON")); err != nil {
		t.Fatalf("perangkat gagal menulis event: %v", err)
	}
	if got := tungguFrameDevice(t, d); got != "IN2ON" {
		t.Fatalf("frame = %q, want IN2ON", got)
	}

	select {
	case err := <-hasil:
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout menunggu Exec selesai")
	}
}

func TestExecPenjagaMembatalkanPerintah(t *testing.T) {
	tolak := errors.New("loop bawah HIGH")
	d, c, _ := siapkanExec(t, WithCommandGuard(func(string) error { return tolak }))

	_, err := d.Exec(context.Background(), "OUT1OFF")
	if !errors.Is(err, tolak) {
		t.Fatalf("Exec = %v, want terbungkus %v", err, tolak)
	}

	select {
	case cmd := <-c.diterima:
		t.Fatalf("perintah %q sampai ke kabel padahal penjaga menolak", cmd)
	case <-time.After(100 * time.Millisecond):
	}
}

// Inti kenapa penjaga ada: kondisi lapangan bisa berubah di tengah rentetan retry,
// jadi ia harus dievaluasi ulang tiap percobaan, bukan sekali di awal (P4 / task 2.3).
func TestExecPenjagaDijalankanTiapPercobaan(t *testing.T) {
	var panggilan atomic.Int32
	d, c, _ := siapkanExec(t, WithCommandGuard(func(string) error {
		panggilan.Add(1)
		return nil
	}))
	c.abaikan.Store(2)

	if _, err := d.Exec(context.Background(), CmdStat); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := panggilan.Load(); got != 3 {
		t.Fatalf("penjaga dipanggil %d kali, want 3 (sekali per percobaan)", got)
	}
}

// Penjaga yang mulai menolak di tengah retry harus menghentikan sisa percobaan.
func TestExecPenjagaMenghentikanRetryDiTengahJalan(t *testing.T) {
	var panggilan atomic.Int32
	tolak := errors.New("interlock aktif")
	d, c, _ := siapkanExec(t, WithCommandGuard(func(string) error {
		if panggilan.Add(1) >= 2 {
			return tolak
		}
		return nil
	}))
	c.abaikan.Store(99)

	_, err := d.Exec(context.Background(), "OUT1OFF")
	if !errors.Is(err, tolak) {
		t.Fatalf("Exec = %v, want terbungkus %v", err, tolak)
	}
	if got := panggilan.Load(); got != 2 {
		t.Fatalf("penjaga dipanggil %d kali, want 2 (berhenti begitu menolak)", got)
	}
}

func TestExecMenolakPerintahTakSah(t *testing.T) {
	d, _, _ := siapkanExec(t)

	if _, err := d.Exec(context.Background(), ""); !errors.Is(err, ErrEmptyCommand) {
		t.Fatalf("Exec(\"\") = %v, want ErrEmptyCommand", err)
	}
	if _, err := d.Exec(context.Background(), "OUT1\xA6ON"); !errors.Is(err, ErrNonASCII) {
		t.Fatalf("Exec(non-ASCII) = %v, want ErrNonASCII", err)
	}
}

func TestExecMenghormatiPembatalanCtx(t *testing.T) {
	d, c, _ := siapkanExec(t)
	c.abaikan.Store(99)

	ctx, batal := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		batal()
	}()

	if _, err := d.Exec(ctx, CmdStat); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec = %v, want context.Canceled", err)
	}
}

// Protokol serial per koneksi: dua Exec bersamaan tidak boleh saling mencuri balasan.
func TestExecSerialSaatDipanggilBersamaan(t *testing.T) {
	d, _, _ := siapkanExec(t)

	const n = 8
	hasil := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := d.Exec(context.Background(), CmdStat)
			hasil <- err
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case err := <-hasil:
			if err != nil {
				t.Fatalf("Exec bersamaan: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout menunggu Exec bersamaan")
		}
	}
	if got := d.Stats().Commands; got != n {
		t.Fatalf("Commands = %d, want %d", got, n)
	}
}

func TestOpsiExecMenolakNilaiTakSah(t *testing.T) {
	d := NewDevice("127.0.0.1:1", WithMaxAttempts(0), WithAckTimeout(0))
	if d.maxAttempts != DefaultMaxAttempts {
		t.Fatalf("maxAttempts = %d, want bawaan %d", d.maxAttempts, DefaultMaxAttempts)
	}
	if d.ackTimeout != DefaultAckTimeout {
		t.Fatalf("ackTimeout = %v, want bawaan %v", d.ackTimeout, DefaultAckTimeout)
	}
}
