// Package tcpctl mengimplementasikan Layer-1 driver controller gerbang A6/A9-TCP
// (PRD v3 §5 & §6.1) — menggantikan codec STX/CRC v2 di paket `protocol` sebagai
// sumber sinyal untuk perangkat lapangan (PRD v3 §6.3).
//
// Format frame:
//
//	Header(0xA6) + Command(ASCII) [+ Data(ASCII)] + Footer(0xA9)
//
// Tanpa LEN, tanpa CRC, tanpa address, tanpa byte-stuffing: byte kontrol 0xA6/0xA9
// berada di atas 127 sedangkan muatan selalu ASCII di bawah 128, sehingga tak pernah
// bentrok. Integritas transport diserahkan ke TCP.
//
// Karena controller tidak dipercaya (P3) dan tidak menyediakan interlock, heartbeat,
// debounce, maupun auto-close (PRD v3 §4), parser di sini bersikap defensif: byte
// sampah dibuang, frame terpotong di-resync, dan buffer dibatasi agar tidak tumbuh
// tanpa batas saat Footer tak pernah tiba.
//
// Cakupan paket ini adalah task 1.1 (framing + klien TCP dasar). Reconnect/backoff
// (1.2), keepalive PING (1.3), korelasi perintah↔respons (1.4), debounce input (1.5),
// dan parser Wiegand (1.6) menyusul di task masing-masing.
package tcpctl

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Byte pembatas frame (PRD v3 §5.2).
const (
	Header byte = 0xA6
	Footer byte = 0xA9
)

// MaxCommandLen membatasi panjang muatan ASCII satu frame. Perintah terpanjang yang
// dikenal kontrak adalah `STATabcdefgh` (12 byte); batas ini memberi ruang untuk
// perluasan vendor sekaligus mencegah buffer tumbuh tanpa batas bila Footer hilang.
const MaxCommandLen = 64

// Batas ASCII yang diterima di dalam frame. Kontrak menyebut "ASCII"; kita persempit
// ke rentang printable agar byte kontrol tak sengaja lolos sebagai perintah palsu.
const (
	asciiMin byte = 0x20
	asciiMax byte = 0x7E
)

var (
	ErrEmptyCommand   = errors.New("tcpctl: perintah kosong")
	ErrCommandTooLong = fmt.Errorf("tcpctl: perintah melebihi %d byte", MaxCommandLen)
	ErrNonASCII       = errors.New("tcpctl: perintah mengandung byte non-ASCII printable")
)

// ── Kosakata perintah (PRD v3 §5.3) ──

// Perintah tanpa argumen kanal.
const (
	CmdPing = "PING"
	CmdStat = "STAT"
)

// Respons/prefiks yang dipakai pemanggil untuk mengenali balasan device (PRD v3 §5.3).
// Korelasi perintah↔respons yang sesungguhnya adalah task 1.4.
const (
	RespPingOK    = "PINGOK"
	PrefixStatAck = "STAT"
	// SuffixAck — device membalas setiap perintah host dengan perintah itu + "OK",
	// mis. `OUT1ON` → `OUT1ONOK`.
	SuffixAck = "OK"
)

// ChannelMin/ChannelMax — controller A6/A9 menyediakan 4 input dan 4 output (PRD v3 §5.3).
const (
	ChannelMin = 1
	ChannelMax = 4
)

// ErrChannelRange dikembalikan bila nomor kanal di luar 1..4.
var ErrChannelRange = fmt.Errorf("tcpctl: kanal di luar rentang %d..%d", ChannelMin, ChannelMax)

// OutOn membangun perintah menyalakan relay output ke-ch (`OUT1ON`..`OUT4ON`).
func OutOn(ch int) (string, error) {
	if err := checkChannel(ch); err != nil {
		return "", err
	}
	return fmt.Sprintf("OUT%dON", ch), nil
}

// OutOff membangun perintah mematikan relay output ke-ch (`OUT1OFF`..`OUT4OFF`).
//
// Catatan keselamatan (P4): untuk kanal palang, `OUT1OFF` adalah perintah TUTUP.
// Interlock — larangan mengirimnya selama loop bawah HIGH — ditegakkan di lapisan
// atas (task 2.3), bukan di sini; paket ini hanya membentuk byte.
func OutOff(ch int) (string, error) {
	if err := checkChannel(ch); err != nil {
		return "", err
	}
	return fmt.Sprintf("OUT%dOFF", ch), nil
}

// Trig membangun perintah pulsa relay 1 detik (`TRIG1`..`TRIG4`), dipakai pada palang
// bermode `pulse` (PRD v3 §6.2).
func Trig(ch int) (string, error) {
	if err := checkChannel(ch); err != nil {
		return "", err
	}
	return fmt.Sprintf("TRIG%d", ch), nil
}

// ParseInput mem-parse event perubahan input `IN1ON`..`IN4OFF` (PRD v3 §5.4).
// ok=false berarti frame bukan event input dan harus dibiarkan lewat apa adanya.
func ParseInput(f string) (ch int, high bool, ok bool) {
	const awalan = "IN"
	if !strings.HasPrefix(f, awalan) {
		return 0, false, false
	}
	sisa := f[len(awalan):]

	switch {
	case strings.HasSuffix(sisa, "OFF"):
		sisa, high = sisa[:len(sisa)-len("OFF")], false
	case strings.HasSuffix(sisa, "ON"):
		sisa, high = sisa[:len(sisa)-len("ON")], true
	default:
		return 0, false, false
	}

	// Tepat satu digit kanal; apa pun selain itu bukan event input yang sah.
	if len(sisa) != 1 || sisa[0] < '0' || sisa[0] > '9' {
		return 0, false, false
	}
	ch = int(sisa[0] - '0')
	if ch < ChannelMin || ch > ChannelMax {
		return 0, false, false
	}
	return ch, high, true
}

// Panjang muatan hex Wiegand yang dikenal kontrak (PRD v3 §5.4).
const (
	wiegand26Hex = 6 // W-26
	wiegand34Hex = 8 // W-34
)

// ParseWiegand mem-parse event tap kartu `Wxxxxxx` (PRD v3 §5.4). Panjang muatan
// membedakan format: 6 hex = W-26, 8 hex = W-34 (auto-deteksi).
//
// UID dikembalikan dalam huruf besar supaya pembanding di lapisan atas tidak perlu
// menormalkan lagi — kartu yang sama tak boleh gagal cocok gara-gara beda kapitalisasi.
func ParseWiegand(f string) (uid string, bits int, ok bool) {
	if len(f) < 2 || f[0] != 'W' {
		return "", 0, false
	}
	muatan := f[1:]

	switch len(muatan) {
	case wiegand26Hex:
		bits = 26
	case wiegand34Hex:
		bits = 34
	default:
		return "", 0, false
	}

	huruf := make([]byte, len(muatan))
	for i := 0; i < len(muatan); i++ {
		c := muatan[i]
		switch {
		case c >= '0' && c <= '9', c >= 'A' && c <= 'F':
			huruf[i] = c
		case c >= 'a' && c <= 'f':
			huruf[i] = c - 'a' + 'A'
		default:
			return "", 0, false // bukan hex → bukan event Wiegand yang sah
		}
	}
	return string(huruf), bits, true
}

func checkChannel(ch int) error {
	if ch < ChannelMin || ch > ChannelMax {
		return fmt.Errorf("%w: %d", ErrChannelRange, ch)
	}
	return nil
}

// ── Codec ──

// Encode membungkus satu perintah ASCII menjadi frame lengkap Header..Footer.
func Encode(cmd string) ([]byte, error) {
	if err := ValidateCommand(cmd); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(cmd)+2)
	out = append(out, Header)
	out = append(out, cmd...)
	out = append(out, Footer)
	return out, nil
}

// ValidateCommand memastikan muatan layak dikirim: tak kosong, tak melebihi batas,
// dan seluruhnya ASCII printable.
func ValidateCommand(cmd string) error {
	if cmd == "" {
		return ErrEmptyCommand
	}
	if len(cmd) > MaxCommandLen {
		return fmt.Errorf("%w: %d byte", ErrCommandTooLong, len(cmd))
	}
	if i := strings.IndexFunc(cmd, func(r rune) bool {
		return r < rune(asciiMin) || r > rune(asciiMax)
	}); i >= 0 {
		return fmt.Errorf("%w: indeks %d", ErrNonASCII, i)
	}
	return nil
}

// ── Parser aliran byte ──

// ParserStats mencatat kejadian abnormal di aliran byte. Angka-angka ini adalah bahan
// telemetry & audit perangkat (task 1.8) sekaligus bukti bahwa kita tidak diam-diam
// menelan frame rusak (P3).
type ParserStats struct {
	Frames     uint64 // frame valid yang berhasil diekstrak
	JunkBytes  uint64 // byte dibuang karena berada di luar Header..Footer
	Truncated  uint64 // Header baru muncul sebelum Footer — frame sebelumnya dibuang
	Oversize   uint64 // muatan melewati MaxCommandLen
	NonASCII   uint64 // byte di luar ASCII printable di dalam frame
	EmptyFrame uint64 // Header langsung diikuti Footer
}

// Parser memotong aliran byte TCP menjadi frame-frame utuh. Parser tidak aman dipakai
// dari beberapa goroutine sekaligus; Client memakainya dari satu goroutine pembaca.
type Parser struct {
	buf   []byte
	stats ParserStats
}

// NewParser membuat parser kosong.
func NewParser() *Parser { return &Parser{} }

// Feed menambahkan potongan byte yang baru dibaca dari socket.
func (p *Parser) Feed(chunk []byte) { p.buf = append(p.buf, chunk...) }

// Next mengembalikan frame utuh berikutnya. ok=false berarti belum ada frame lengkap
// di buffer — panggil Feed lagi setelah membaca socket.
func (p *Parser) Next() (cmd string, ok bool) {
	for {
		start := bytes.IndexByte(p.buf, Header)
		if start < 0 {
			// Tak ada Header sama sekali: seluruh isi buffer adalah sampah.
			p.stats.JunkBytes += uint64(len(p.buf))
			p.buf = p.buf[:0]
			return "", false
		}
		if start > 0 {
			// Byte sebelum Header tak pernah menjadi bagian frame valid.
			p.stats.JunkBytes += uint64(start)
			p.buf = p.buf[start:]
		}

		end, resync := -1, -1
		for i := 1; i < len(p.buf); i++ {
			if p.buf[i] == Footer {
				end = i
				break
			}
			if p.buf[i] == Header {
				// Header baru sebelum Footer → frame lama terpotong (device reboot
				// di tengah kirim, atau byte hilang). Buang, mulai dari Header baru.
				resync = i
				break
			}
		}

		if resync >= 0 {
			p.stats.Truncated++
			p.stats.JunkBytes += uint64(resync)
			p.buf = p.buf[resync:]
			continue
		}

		if end < 0 {
			// Footer belum tiba. Bila calon muatan sudah melebihi batas, frame ini
			// tak akan pernah sah — buang Header-nya dan resync.
			if len(p.buf)-1 > MaxCommandLen {
				p.stats.Oversize++
				p.stats.JunkBytes++
				p.buf = p.buf[1:]
				continue
			}
			return "", false // tunggu byte berikutnya
		}

		payload := p.buf[1:end]
		p.buf = p.buf[end+1:]

		switch {
		case len(payload) == 0:
			p.stats.EmptyFrame++
			continue
		case len(payload) > MaxCommandLen:
			p.stats.Oversize++
			continue
		case !isPrintableASCII(payload):
			p.stats.NonASCII++
			continue
		}

		p.stats.Frames++
		return string(payload), true
	}
}

// Stats mengembalikan salinan pencacah parser.
func (p *Parser) Stats() ParserStats { return p.stats }

// Buffered melaporkan jumlah byte yang masih menunggu Footer — berguna untuk diagnosa
// device yang mengirim frame tak lengkap.
func (p *Parser) Buffered() int { return len(p.buf) }

// Reset mengosongkan buffer tanpa menghapus statistik. Dipanggil saat koneksi diputus
// agar sisa frame separuh tidak menyeberang ke sesi berikutnya (task 1.2).
func (p *Parser) Reset() { p.buf = p.buf[:0] }

func isPrintableASCII(b []byte) bool {
	for _, c := range b {
		if c < asciiMin || c > asciiMax {
			return false
		}
	}
	return true
}
