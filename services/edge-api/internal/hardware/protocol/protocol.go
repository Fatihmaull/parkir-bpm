// Package protocol mengimplementasikan Layer 1 (codec frame) dari kontrak perangkat PRD §5.3.
// Format frame: STX | ADDR | CMD | LEN | PAYLOAD | CRC16(LE) | ETX.
// CRC-16/MODBUS atas ADDR..PAYLOAD. Byte 0x02/0x03/0x10 dalam payload di-escape.
package protocol

import (
	"errors"
	"fmt"
)

// Byte kontrol frame.
const (
	STX    byte = 0x02
	ETX    byte = 0x03
	ESC    byte = 0x10
	ESCXOR byte = 0x20
)

// Alamat controller (PRD §5.1).
const (
	AddrGateIn     byte = 0x01
	AddrGateOut    byte = 0x02
	AddrBroadcast  byte = 0xFF
)

// Perintah PC → Controller (PRD §5.3.2).
const (
	CmdGateOpen     byte = 0x01
	CmdGateClose    byte = 0x02
	CmdGateQuery    byte = 0x03
	CmdLightSet     byte = 0x10
	CmdLoopQuery    byte = 0x20
	CmdPrintTicket  byte = 0x30
	CmdPrinterQuery byte = 0x31
	CmdTicketRetract byte = 0x32
	CmdHeartbeat    byte = 0xF0
	CmdReset        byte = 0xF1
)

// Event tak diminta Controller → PC (PRD §5.3.3).
const (
	EvtGateState    byte = 0x04
	EvtLoopEvent    byte = 0x21
	EvtPrinterEvent byte = 0x33
	EvtRFIDTap      byte = 0x40
	EvtButtonPress  byte = 0x50
	EvtFault        byte = 0xFE
)

// Pola lampu (payload LIGHT_SET, PRD §5.3.2).
const (
	LightOff       byte = 0x00
	LightRed       byte = 0x01
	LightGreen     byte = 0x02
	LightYellowBlink byte = 0x03
	LightRedBlink  byte = 0x04
)

// Kode FAULT (PRD §5.3.4).
const (
	FaultSafetyInterlock  byte = 0x01
	FaultAutocloseTimeout byte = 0x02
)

var (
	ErrShortFrame  = errors.New("protocol: frame terlalu pendek")
	ErrBadSTX      = errors.New("protocol: STX tidak valid")
	ErrBadETX      = errors.New("protocol: ETX tidak valid")
	ErrBadCRC      = errors.New("protocol: CRC16 tidak cocok")
	ErrBadLen      = errors.New("protocol: LEN tidak cocok dengan panjang payload")
	ErrPayloadTooLong = errors.New("protocol: payload > 255 byte")
)

// Frame merepresentasikan satu frame protokol yang sudah ter-decode.
type Frame struct {
	Addr    byte
	Cmd     byte
	Payload []byte
}

// Encode membangun byte frame lengkap (dengan STX/ETX, CRC, dan byte-stuffing payload).
func Encode(f Frame) ([]byte, error) {
	if len(f.Payload) > 255 {
		return nil, ErrPayloadTooLong
	}
	// CRC dihitung atas ADDR..PAYLOAD (data mentah, sebelum stuffing).
	body := make([]byte, 0, 3+len(f.Payload))
	body = append(body, f.Addr, f.Cmd, byte(len(f.Payload)))
	body = append(body, f.Payload...)
	crc := crc16Modbus(body)

	out := make([]byte, 0, len(body)+5)
	out = append(out, STX)
	out = appendStuffed(out, body)
	out = appendStuffed(out, []byte{byte(crc), byte(crc >> 8)}) // little-endian
	out = append(out, ETX)
	return out, nil
}

// Decode mem-parse satu frame lengkap (STX..ETX) menjadi Frame, membuka byte-stuffing & memverifikasi CRC.
func Decode(raw []byte) (Frame, error) {
	if len(raw) < 6 { // STX + ADDR + CMD + LEN + CRC(2) + ETX minimum tanpa payload = 6
		return Frame{}, ErrShortFrame
	}
	if raw[0] != STX {
		return Frame{}, ErrBadSTX
	}
	if raw[len(raw)-1] != ETX {
		return Frame{}, ErrBadETX
	}
	inner, err := unstuff(raw[1 : len(raw)-1])
	if err != nil {
		return Frame{}, err
	}
	if len(inner) < 5 { // ADDR+CMD+LEN+CRC(2)
		return Frame{}, ErrShortFrame
	}
	addr, cmd, ln := inner[0], inner[1], int(inner[2])
	if len(inner) != 3+ln+2 {
		return Frame{}, ErrBadLen
	}
	payload := inner[3 : 3+ln]
	gotCRC := uint16(inner[3+ln]) | uint16(inner[3+ln+1])<<8
	wantCRC := crc16Modbus(inner[:3+ln])
	if gotCRC != wantCRC {
		return Frame{}, fmt.Errorf("%w: got=%04X want=%04X", ErrBadCRC, gotCRC, wantCRC)
	}
	return Frame{Addr: addr, Cmd: cmd, Payload: append([]byte(nil), payload...)}, nil
}

// appendStuffed menambahkan b ke dst dengan escaping byte STX/ETX/ESC.
func appendStuffed(dst, b []byte) []byte {
	for _, x := range b {
		if x == STX || x == ETX || x == ESC {
			dst = append(dst, ESC, x^ESCXOR)
		} else {
			dst = append(dst, x)
		}
	}
	return dst
}

// unstuff membalik byte-stuffing.
func unstuff(b []byte) ([]byte, error) {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == ESC {
			if i+1 >= len(b) {
				return nil, fmt.Errorf("protocol: ESC menggantung di akhir frame")
			}
			out = append(out, b[i+1]^ESCXOR)
			i++
		} else {
			out = append(out, b[i])
		}
	}
	return out, nil
}

// crc16Modbus menghitung CRC-16/MODBUS (poly 0xA001, init 0xFFFF).
func crc16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
