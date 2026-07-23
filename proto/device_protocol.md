# KONTRAK PROTOKOL PERANGKAT — Gerbang ↔ Edge Node

**Deliverable formal ke tim controller lapangan (PRD §5.3).**
Selama sisi controller mematuhi kontrak ini, kedua sisi (PC & firmware) dapat dikembangkan paralel
tanpa saling menunggu. Versi: 1.0. Basis: PRD Pondasi §5.3.

---

## 1. Transport & Peta Port (PRD §5.1)

| Kanal | Transport | Perangkat | Alamat |
|-------|-----------|-----------|--------|
| `COM3` | RS232, 9600 8N1 | Controller Gerbang Masuk: LD1, LD2, palang, lampu, RFID Mifare, printer | `0x01` |
| `COM4` | RS232, 9600 8N1 | Controller Gerbang Keluar: LD3, LD4, palang, lampu | `0x02` |
| `TCP :5001` | TCP/IP | EDC Bank Reader Bridge | — |
| `gRPC :50051` | gRPC | LPR Service | — |
| `RTSP` | RTSP/ONVIF | IP Camera masuk & keluar | per-gate config |

---

## 2. Format Frame

```
┌─────┬──────┬─────┬─────┬───────────┬────────┬─────┐
│ STX │ ADDR │ CMD │ LEN │  PAYLOAD  │ CRC16  │ ETX │
│0x02 │  1B  │ 1B  │ 1B  │  0–255 B  │  2B LE │0x03 │
└─────┴──────┴─────┴─────┴───────────┴────────┴─────┘
```

- `ADDR` — `0x01` controller masuk, `0x02` controller keluar, `0xFF` broadcast.
- `CRC16` — CRC-16/MODBUS, dihitung atas `ADDR..PAYLOAD` (inklusif), little-endian.
- **Byte-stuffing:** byte `0x02`, `0x03`, `0x10` di dalam PAYLOAD di-*escape* dengan
  `0x10` diikuti (byte XOR `0x20`).

---

## 3. Perintah PC → Controller

| CMD | Nama | Payload | Respons | Timeout |
|-----|------|---------|---------|---------|
| `0x01` | `GATE_OPEN` | `[durasi_pulse_ms:2B]` | `ACK` + `GATE_STATE` | 500 ms |
| `0x02` | `GATE_CLOSE` | — | `ACK` + `GATE_STATE` | 500 ms |
| `0x03` | `GATE_QUERY` | — | `GATE_STATE` | 300 ms |
| `0x10` | `LIGHT_SET` | `[pola:1B]` 00=off 01=merah 02=hijau 03=kuning-blink 04=merah-blink | `ACK` | 300 ms |
| `0x20` | `LOOP_QUERY` | `[loop_id:1B]` | `LOOP_STATE` | 300 ms |
| `0x30` | `PRINT_TICKET` | `[len:2B][data ESC/POS]` | `ACK` lalu `PRINT_DONE` | 300 ms / 2000 ms |
| `0x31` | `PRINTER_QUERY` | — | `PRINTER_STATE` | 300 ms |
| `0x32` | `TICKET_RETRACT` | — | `ACK` | 500 ms |
| `0xF0` | `HEARTBEAT` | `[epoch_ms:8B]` | `HEARTBEAT_ACK` | 500 ms |
| `0xF1` | `RESET` | — | `ACK` | 2000 ms |

## 4. Event Tak Diminta (Controller → PC)

Controller **mengirim sendiri** tanpa diminta — polling tidak memenuhi budget latensi (NFR-1).

| CMD | Nama | Payload | Kapan dikirim |
|-----|------|---------|---------------|
| `0x21` | `LOOP_EVENT` | `[loop_id:1B][state:1B][ts:8B]` | Perubahan state loop, **sudah ter-debounce ≥150 ms di controller** |
| `0x33` | `PRINTER_EVENT` | `[event:1B]` 01=TICKET_TAKEN 02=PAPER_LOW 03=PAPER_OUT 04=JAM | Perubahan status printer |
| `0x40` | `RFID_TAP` | `[uid_len:1B][uid:nB][ts:8B]` | Kartu Mifare terbaca |
| `0x50` | `BUTTON_PRESS` | `[button_id:1B][ts:8B]` | Tombol ambil tiket ditekan |
| `0x04` | `GATE_STATE` | `[state:1B]` 00=closed 01=open 02=moving 03=fault | Perubahan posisi palang |
| `0xFE` | `FAULT` | `[kode:1B][detail:nB]` | Kondisi abnormal |

**Kode FAULT:** `0x01` SAFETY_INTERLOCK · `0x02` AUTOCLOSE_TIMEOUT · (lainnya per implementasi).

---

## 5. Kewajiban Sisi Controller (NON-NEGOTIABLE)

Wajib di firmware — tidak boleh hanya mengandalkan PC:

1. **Interlock keselamatan (P4).** Palang DILARANG menutup selama loop bawah (LD2/LD4) HIGH —
   bahkan jika PC mengirim `GATE_CLOSE`. Controller menolak & membalas `FAULT 0x01 SAFETY_INTERLOCK`.
2. **Fail-safe kehilangan komunikasi.** Tanpa `HEARTBEAT` selama **5 detik** → mode aman: palang
   ditutup (jika loop bawah LOW), lampu merah berkedip, tombol tiket dinonaktifkan. Controller
   TIDAK boleh membuka palang atas inisiatif sendiri.
3. **Debounce loop detector ≥150 ms** di controller. PC menerima sinyal yang sudah bersih.
4. **Auto-close pengaman.** Palang terbuka > **60 detik** tanpa perintah & loop bawah LOW →
   controller menutup sendiri + `FAULT 0x02 AUTOCLOSE_TIMEOUT`.
5. **Idempotensi.** `GATE_OPEN` pada palang yang sudah terbuka → `ACK` tanpa efek samping
   (bukan error, bukan pulse ganda).

## 6. Kewajiban Sisi PC (Kita)

- Kirim `HEARTBEAT` setiap **1 detik**. Tandai controller `OFFLINE` setelah **3 heartbeat** tanpa
  balasan (3 detik).
- Retry perintah maksimal **3×** dengan backoff 100/200/400 ms → lalu `FAULT` + alert.
- Setiap frame TX/RX dicatat ke ring buffer telemetry (Hardware Config, PRD §12.11) dan ke
  `audit_logs` bila `severity ≥ warning`.

---

## 7. Simulator (P7)

Simulator perangkat lengkap (`sim` transport) mengimplementasikan seluruh kontrak di atas sehingga
setiap logika Edge dapat dites tanpa perangkat fisik. Firmware lapangan dapat diuji terhadap Edge
menggunakan kontrak yang sama. Lihat `services/edge-api/internal/hardware/`.
