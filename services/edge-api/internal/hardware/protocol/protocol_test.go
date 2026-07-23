package protocol

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Frame{
		{Addr: AddrGateIn, Cmd: CmdGateOpen, Payload: []byte{0xE8, 0x03}}, // pulse 1000ms
		{Addr: AddrGateOut, Cmd: CmdGateClose, Payload: nil},
		{Addr: AddrGateIn, Cmd: CmdLightSet, Payload: []byte{LightGreen}},
		// Payload berisi byte kontrol → wajib ter-escape dan pulih utuh.
		{Addr: AddrGateIn, Cmd: CmdPrintTicket, Payload: []byte{STX, ETX, ESC, 0x41, 0x42}},
	}
	for i, in := range cases {
		raw, err := Encode(in)
		if err != nil {
			t.Fatalf("case %d: Encode: %v", i, err)
		}
		if raw[0] != STX || raw[len(raw)-1] != ETX {
			t.Fatalf("case %d: frame tidak dibungkus STX/ETX", i)
		}
		out, err := Decode(raw)
		if err != nil {
			t.Fatalf("case %d: Decode: %v", i, err)
		}
		if out.Addr != in.Addr || out.Cmd != in.Cmd || !bytes.Equal(out.Payload, in.Payload) {
			t.Fatalf("case %d: round-trip mismatch: in=%+v out=%+v", i, in, out)
		}
	}
}

func TestDecodeRejectsCorruptCRC(t *testing.T) {
	raw, _ := Encode(Frame{Addr: AddrGateIn, Cmd: CmdGateQuery})
	// Balikkan satu bit di body (indeks 2 = ADDR setelah STX).
	corrupt := append([]byte(nil), raw...)
	corrupt[2] ^= 0xFF
	if _, err := Decode(corrupt); err == nil {
		t.Fatal("Decode seharusnya menolak frame dengan CRC rusak")
	}
}

func TestCRC16ModbusKnownVector(t *testing.T) {
	// Vektor uji CRC-16/MODBUS standar: "123456789" → 0x4B37.
	if got := crc16Modbus([]byte("123456789")); got != 0x4B37 {
		t.Fatalf("crc16Modbus = %04X, want 4B37", got)
	}
}
