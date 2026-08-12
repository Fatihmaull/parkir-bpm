package tcpctl

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEncodeMembungkusHeaderFooter(t *testing.T) {
	got, err := Encode("OUT1ON")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := append([]byte{Header}, append([]byte("OUT1ON"), Footer)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode = % X, want % X", got, want)
	}
}

func TestEncodeMenolakMuatanTakSah(t *testing.T) {
	cases := []struct {
		nama string
		cmd  string
		want error
	}{
		{"kosong", "", ErrEmptyCommand},
		{"terlalu panjang", strings.Repeat("A", MaxCommandLen+1), ErrCommandTooLong},
		{"non-ASCII", "OUT1\xA6ON", ErrNonASCII},
		{"byte kontrol", "OUT1\x00ON", ErrNonASCII},
		{"unicode", "PALANG→BUKA", ErrNonASCII},
	}
	for _, tc := range cases {
		t.Run(tc.nama, func(t *testing.T) {
			if _, err := Encode(tc.cmd); !errors.Is(err, tc.want) {
				t.Fatalf("Encode(%q) error = %v, want %v", tc.cmd, err, tc.want)
			}
		})
	}
}

func TestPembangunPerintahKanal(t *testing.T) {
	cases := []struct {
		fn   func(int) (string, error)
		ch   int
		want string
	}{
		{OutOn, 1, "OUT1ON"},
		{OutOn, 4, "OUT4ON"},
		{OutOff, 1, "OUT1OFF"},
		{OutOff, 3, "OUT3OFF"},
		{Trig, 2, "TRIG2"},
	}
	for _, tc := range cases {
		got, err := tc.fn(tc.ch)
		if err != nil {
			t.Fatalf("kanal %d: %v", tc.ch, err)
		}
		if got != tc.want {
			t.Fatalf("= %q, want %q", got, tc.want)
		}
	}

	// Kanal di luar 1..4 harus ditolak, bukan menghasilkan perintah ngawur ke perangkat (P3).
	for _, ch := range []int{0, 5, -1, 99} {
		if _, err := OutOn(ch); !errors.Is(err, ErrChannelRange) {
			t.Fatalf("OutOn(%d) error = %v, want ErrChannelRange", ch, err)
		}
		if _, err := OutOff(ch); !errors.Is(err, ErrChannelRange) {
			t.Fatalf("OutOff(%d) error = %v, want ErrChannelRange", ch, err)
		}
		if _, err := Trig(ch); !errors.Is(err, ErrChannelRange) {
			t.Fatalf("Trig(%d) error = %v, want ErrChannelRange", ch, err)
		}
	}
}

// frame membantu menyusun byte frame mentah di dalam tes.
func frame(cmd string) []byte {
	return append([]byte{Header}, append([]byte(cmd), Footer)...)
}

// drain mengambil semua frame yang siap dari parser.
func drain(p *Parser) []string {
	var out []string
	for {
		cmd, ok := p.Next()
		if !ok {
			return out
		}
		out = append(out, cmd)
	}
}

func TestParserSatuFrame(t *testing.T) {
	p := NewParser()
	p.Feed(frame("PINGOK"))
	got := drain(p)
	if len(got) != 1 || got[0] != "PINGOK" {
		t.Fatalf("= %q, want [PINGOK]", got)
	}
	if p.Buffered() != 0 {
		t.Fatalf("buffer tersisa %d byte", p.Buffered())
	}
}

func TestParserBanyakFrameSatuPotongan(t *testing.T) {
	p := NewParser()
	var chunk []byte
	for _, c := range []string{"IN1ON", "IN2ON", "W1A2B3C", "STAT11000100"} {
		chunk = append(chunk, frame(c)...)
	}
	p.Feed(chunk)

	got := drain(p)
	want := []string{"IN1ON", "IN2ON", "W1A2B3C", "STAT11000100"}
	if len(got) != len(want) {
		t.Fatalf("dapat %d frame (%q), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d = %q, want %q", i, got[i], want[i])
		}
	}
	if p.Stats().Frames != 4 {
		t.Fatalf("Stats().Frames = %d, want 4", p.Stats().Frames)
	}
}

// TCP tidak menjamin batas pesan: satu frame bisa tiba terpecah byte per byte.
func TestParserFrameTerpecahAntarPotongan(t *testing.T) {
	p := NewParser()
	raw := frame("OUT1ONOK")

	for i, b := range raw {
		p.Feed([]byte{b})
		got := drain(p)
		if i < len(raw)-1 {
			if len(got) != 0 {
				t.Fatalf("byte ke-%d: frame keluar terlalu cepat: %q", i, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != "OUT1ONOK" {
			t.Fatalf("byte terakhir: = %q, want [OUT1ONOK]", got)
		}
	}
}

func TestParserBuangSampahSebelumHeader(t *testing.T) {
	p := NewParser()
	// Sisa byte sesi sebelumnya / derau saluran mendahului frame yang sah.
	p.Feed(append([]byte{0x00, 0xFF, 0x41}, frame("IN2OFF")...))

	got := drain(p)
	if len(got) != 1 || got[0] != "IN2OFF" {
		t.Fatalf("= %q, want [IN2OFF]", got)
	}
	if p.Stats().JunkBytes != 3 {
		t.Fatalf("JunkBytes = %d, want 3", p.Stats().JunkBytes)
	}
}

func TestParserResyncSaatFrameTerpotong(t *testing.T) {
	p := NewParser()
	// Device reboot di tengah kirim: Header baru muncul sebelum Footer frame pertama.
	p.Feed([]byte{Header, 'I', 'N', '1', 'O'})
	p.Feed(frame("IN3ON"))

	got := drain(p)
	if len(got) != 1 || got[0] != "IN3ON" {
		t.Fatalf("= %q, want [IN3ON]", got)
	}
	st := p.Stats()
	if st.Truncated != 1 {
		t.Fatalf("Truncated = %d, want 1", st.Truncated)
	}
	if st.Frames != 1 {
		t.Fatalf("Frames = %d, want 1", st.Frames)
	}
}

func TestParserTolakFrameKosong(t *testing.T) {
	p := NewParser()
	p.Feed([]byte{Header, Footer})
	p.Feed(frame("PINGOK"))

	got := drain(p)
	if len(got) != 1 || got[0] != "PINGOK" {
		t.Fatalf("= %q, want [PINGOK]", got)
	}
	if p.Stats().EmptyFrame != 1 {
		t.Fatalf("EmptyFrame = %d, want 1", p.Stats().EmptyFrame)
	}
}

func TestParserTolakMuatanNonASCII(t *testing.T) {
	p := NewParser()
	// 0x80 bukan Header maupun Footer, jadi frame tampak utuh tetapi muatannya rusak.
	p.Feed([]byte{Header, 'A', 'B', 0x80, 'C', Footer})
	p.Feed(frame("PINGOK"))

	got := drain(p)
	if len(got) != 1 || got[0] != "PINGOK" {
		t.Fatalf("= %q, want [PINGOK]", got)
	}
	if p.Stats().NonASCII != 1 {
		t.Fatalf("NonASCII = %d, want 1", p.Stats().NonASCII)
	}
}

func TestParserTolakFrameKepanjangan(t *testing.T) {
	p := NewParser()
	p.Feed(frame(strings.Repeat("A", MaxCommandLen+1)))
	p.Feed(frame("PINGOK"))

	got := drain(p)
	if len(got) != 1 || got[0] != "PINGOK" {
		t.Fatalf("= %q, want [PINGOK]", got)
	}
	if p.Stats().Oversize != 1 {
		t.Fatalf("Oversize = %d, want 1", p.Stats().Oversize)
	}
}

// Footer yang tak pernah tiba tidak boleh membuat buffer tumbuh tanpa batas (P3):
// begitu calon muatan melewati batas, parser membuang Header dan resync.
func TestParserBufferTidakTumbuhTanpaBatas(t *testing.T) {
	p := NewParser()
	p.Feed([]byte{Header})
	for i := 0; i < 100; i++ {
		p.Feed(bytes.Repeat([]byte("A"), 64))
		drain(p)
		if p.Buffered() > MaxCommandLen+1 {
			t.Fatalf("iterasi %d: buffer membengkak ke %d byte", i, p.Buffered())
		}
	}
	if p.Stats().Oversize == 0 {
		t.Fatal("Oversize seharusnya tercatat")
	}

	// Parser harus tetap sehat setelah membuang sampah sebanyak itu.
	p.Feed(frame("PINGOK"))
	if got := drain(p); len(got) != 1 || got[0] != "PINGOK" {
		t.Fatalf("setelah resync: = %q, want [PINGOK]", got)
	}
}

func TestParserResetKosongkanBufferPertahankanStatistik(t *testing.T) {
	p := NewParser()
	p.Feed(frame("IN1ON"))
	drain(p)
	p.Feed([]byte{Header, 'I', 'N'}) // frame separuh

	if p.Buffered() == 0 {
		t.Fatal("buffer seharusnya menyimpan frame separuh")
	}
	p.Reset()
	if p.Buffered() != 0 {
		t.Fatalf("setelah Reset buffer = %d byte, want 0", p.Buffered())
	}
	if p.Stats().Frames != 1 {
		t.Fatalf("Reset tidak boleh menghapus statistik: Frames = %d, want 1", p.Stats().Frames)
	}
}

// Round-trip: apa yang kita Encode harus terbaca utuh oleh parser di seberang.
func TestRoundTripEncodeParser(t *testing.T) {
	cmds := []string{"OUT1ON", "OUT4OFF", "TRIG3", CmdPing, CmdStat, "W1A2B3C4"}
	p := NewParser()
	for _, c := range cmds {
		raw, err := Encode(c)
		if err != nil {
			t.Fatalf("Encode(%q): %v", c, err)
		}
		p.Feed(raw)
	}
	got := drain(p)
	if len(got) != len(cmds) {
		t.Fatalf("dapat %d frame (%q), want %d", len(got), got, len(cmds))
	}
	for i := range cmds {
		if got[i] != cmds[i] {
			t.Fatalf("frame %d = %q, want %q", i, got[i], cmds[i])
		}
	}
}
