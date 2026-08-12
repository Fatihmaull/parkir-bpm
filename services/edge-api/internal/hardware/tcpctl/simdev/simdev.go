// Package simdev menyediakan simulator controller gerbang A6/A9-TCP (prinsip P7 —
// semua perangkat punya simulator).
//
// Simulator ini berbicara protokol yang sama dengan controller lapangan: mendengarkan
// TCP, membalas OUTxON/OFF, TRIGx, PING, dan STAT, serta mengirim event tak diminta
// INxON/OFF dan Wiegand. Karena murni Go dan berjalan dalam proses, ia dapat dipakai
// di uji integrasi CI tanpa perangkat keras (task 1.10) maupun saat pengembangan lokal.
//
// Ia juga bisa disuruh berperilaku buruk — membisu tanpa menutup socket, atau memutus
// koneksi sepihak — supaya jalur pemulihan driver benar-benar teruji, bukan diasumsikan.
package simdev

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
)

// DefaultPulse — lama relay menyala pada perintah TRIGx (PRD v3 §5.3).
const DefaultPulse = 1 * time.Second

const kanalMax = 4

// Device adalah satu controller gerbang tersimulasi.
type Device struct {
	ln    net.Listener
	pulse time.Duration

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	inputs  [kanalMax]bool
	outputs [kanalMax]bool
	bisu    bool

	tutupSekali sync.Once
	tutup       chan struct{}
	wg          sync.WaitGroup
}

// Option menyetel simulator.
type Option func(*Device)

// WithPulse mengatur lama relay menyala pada TRIGx. Berguna agar tes tak perlu
// menunggu satu detik penuh.
func WithPulse(d time.Duration) Option {
	return func(s *Device) {
		if d > 0 {
			s.pulse = d
		}
	}
}

// New menjalankan simulator pada addr. addr kosong berarti port bebas di localhost.
func New(addr string, opts ...Option) (*Device, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("simdev: gagal mendengarkan di %s: %w", addr, err)
	}

	s := &Device{
		ln:    ln,
		pulse: DefaultPulse,
		conns: make(map[net.Conn]struct{}),
		tutup: make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}

	s.wg.Add(1)
	go s.terimaKoneksi()
	return s, nil
}

// Addr mengembalikan alamat dengar simulator.
func (s *Device) Addr() string { return s.ln.Addr().String() }

// Close menghentikan simulator dan memutus seluruh koneksi.
func (s *Device) Close() error {
	s.tutupSekali.Do(func() {
		close(s.tutup)
		_ = s.ln.Close()
		s.putusSemua()
	})
	s.wg.Wait()
	return nil
}

// SetInput menggerakkan satu kanal input dan menyiarkan perubahannya, meniru loop
// detector atau tombol. Perubahan ke nilai yang sama tidak menghasilkan event.
func (s *Device) SetInput(ch int, high bool) error {
	if ch < 1 || ch > kanalMax {
		return fmt.Errorf("simdev: kanal input %d di luar 1..%d", ch, kanalMax)
	}
	s.mu.Lock()
	if s.inputs[ch-1] == high {
		s.mu.Unlock()
		return nil
	}
	s.inputs[ch-1] = high
	s.mu.Unlock()

	akhiran := "OFF"
	if high {
		akhiran = "ON"
	}
	s.siarkan(fmt.Sprintf("IN%d%s", ch, akhiran))
	return nil
}

// Pantul meniru pantulan kontak: nilai berganti-ganti cepat sebelum diam di akhir.
// Berguna untuk membuktikan penyaring debounce benar-benar bekerja.
func (s *Device) Pantul(ch int, kali int, jeda time.Duration, akhir bool) error {
	for i := 0; i < kali; i++ {
		if err := s.SetInput(ch, !akhir); err != nil {
			return err
		}
		time.Sleep(jeda)
		if err := s.SetInput(ch, akhir); err != nil {
			return err
		}
		time.Sleep(jeda)
	}
	return s.SetInput(ch, akhir)
}

// Tap menyiarkan pembacaan kartu Wiegand.
func (s *Device) Tap(uid string) { s.siarkan("W" + strings.ToUpper(uid)) }

// Output melaporkan posisi relay output.
func (s *Device) Output(ch int) bool {
	if ch < 1 || ch > kanalMax {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputs[ch-1]
}

// Input melaporkan posisi kanal input.
func (s *Device) Input(ch int) bool {
	if ch < 1 || ch > kanalMax {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputs[ch-1]
}

// Diamkan membuat controller berhenti membalas perintah TANPA menutup socket —
// meniru controller yang hang, kegagalan yang justru jadi alasan keepalive ada.
func (s *Device) Diamkan(bisu bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bisu = bisu
}

// PutusKoneksi memutus seluruh koneksi yang sedang terbuka, meniru kabel LAN dicabut
// atau controller reboot. Simulator tetap mendengarkan sehingga Edge dapat menyambung
// kembali.
func (s *Device) PutusKoneksi() { s.putusSemua() }

// Terkoneksi melaporkan jumlah koneksi yang sedang terbuka.
func (s *Device) Terkoneksi() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Device) terimaKoneksi() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener ditutup
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go s.layani(conn)
	}
}

func (s *Device) layani(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	p := tcpctl.NewParser()
	buf := make([]byte, 512)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			p.Feed(buf[:n])
			for {
				cmd, ok := p.Next()
				if !ok {
					break
				}
				if balas, kirim := s.tangani(cmd); kirim {
					if err := s.kirim(conn, balas); err != nil {
						return
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// tangani menerapkan satu perintah dan mengembalikan balasannya.
func (s *Device) tangani(cmd string) (balas string, kirim bool) {
	s.mu.Lock()
	bisu := s.bisu
	s.mu.Unlock()
	if bisu {
		return "", false // controller hang: perintah masuk, tak ada yang terjadi
	}

	switch {
	case cmd == tcpctl.CmdPing:
		return tcpctl.RespPingOK, true

	case cmd == tcpctl.CmdStat:
		return s.stat(), true

	case strings.HasPrefix(cmd, "TRIG"):
		ch, ok := kanalDari(cmd, "TRIG", "")
		if !ok {
			return "", false
		}
		s.setOutput(ch, true)
		go s.padamkanSetelahPulsa(ch)
		return cmd + tcpctl.SuffixAck, true

	case strings.HasPrefix(cmd, "OUT") && strings.HasSuffix(cmd, "OFF"):
		ch, ok := kanalDari(cmd, "OUT", "OFF")
		if !ok {
			return "", false
		}
		s.setOutput(ch, false)
		return cmd + tcpctl.SuffixAck, true

	case strings.HasPrefix(cmd, "OUT") && strings.HasSuffix(cmd, "ON"):
		ch, ok := kanalDari(cmd, "OUT", "ON")
		if !ok {
			return "", false
		}
		s.setOutput(ch, true)
		return cmd + tcpctl.SuffixAck, true
	}

	// Perintah tak dikenal diabaikan diam-diam, seperti controller sungguhan.
	return "", false
}

func (s *Device) padamkanSetelahPulsa(ch int) {
	t := time.NewTimer(s.pulse)
	defer t.Stop()
	select {
	case <-t.C:
		s.setOutput(ch, false)
	case <-s.tutup:
	}
}

func (s *Device) setOutput(ch int, nyala bool) {
	if ch < 1 || ch > kanalMax {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs[ch-1] = nyala
}

// stat menyusun balasan STATabcdefgh (PRD v3 §5.5).
func (s *Device) stat() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var b strings.Builder
	b.WriteString(tcpctl.PrefixStatAck)
	for _, v := range s.inputs {
		b.WriteByte(bitDari(v))
	}
	for _, v := range s.outputs {
		b.WriteByte(bitDari(v))
	}
	return b.String()
}

func (s *Device) siarkan(muatan string) {
	s.mu.Lock()
	bisu := s.bisu
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if bisu {
		return
	}
	for _, c := range conns {
		_ = s.kirim(c, muatan)
	}
}

func (s *Device) kirim(conn net.Conn, muatan string) error {
	f, err := tcpctl.Encode(muatan)
	if err != nil {
		return err
	}
	_, err = conn.Write(f)
	return err
}

func (s *Device) putusSemua() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

// kanalDari mengambil nomor kanal dari perintah berpola <awalan><n><akhiran>.
func kanalDari(cmd, awalan, akhiran string) (int, bool) {
	sisa := strings.TrimPrefix(cmd, awalan)
	sisa = strings.TrimSuffix(sisa, akhiran)
	ch, err := strconv.Atoi(sisa)
	if err != nil || ch < 1 || ch > kanalMax {
		return 0, false
	}
	return ch, true
}

func bitDari(v bool) byte {
	if v {
		return '1'
	}
	return '0'
}
