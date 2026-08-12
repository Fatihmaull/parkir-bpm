# KONTRAK PERANGKAT — Gate Controller A6/A9-TCP ↔ Edge Node

> **STATUS: DRAFT — BELUM DITERBITKAN.**
> Dokumen ini **belum** dikirim ke tim instalasi lapangan. Masih ada item terbuka
> (H2, H3, H5 — lihat §9) yang harus dikonfirmasi ke vendor/klien sebelum versi final diterbitkan.
> Jangan bagikan ke pihak ketiga sebelum item tersebut tertutup.

**Versi:** 2.0-draft · **Basis:** `docs/PRD_v3_ENTERPRISE.md` §5–§6 · **Menggantikan:** v1.0 (kontrak
STX/ADDR/LEN/CRC16/ETX di atas RS232 — dibatalkan, lihat §10).

**Audiens:** tim instalasi lapangan (pemasangan, wiring, peta pin, jaringan lahan).

**Sifat dokumen:** ini **bukan** spesifikasi yang kami paksakan ke penulis firmware. Protokol A6/A9
sudah ditetapkan vendor controller (spec vendor v1.0); dokumen ini adalah **formalisasi kami atas
spec tersebut** ditambah keputusan integrasi kami (peta pin, mode palang, apa yang ditegakkan Edge).
Bila dokumen ini berbeda dari spec vendor, **spec vendor yang menang** — laporkan selisihnya ke kami.

---

## 1. Ruang Lingkup

Dokumen ini mencakup **hanya gate controller A6/A9-TCP** (4 input digital, 4 output relay, 1 port
Wiegand).

Perangkat berikut **tidak** memakai kontrak ini dan dijelaskan terpisah:

| Perangkat | Status kontrak |
|-----------|----------------|
| Mesin Tiket Otomatis (printer) | **Belum ditetapkan** — menunggu merek/protokol (H1). Bukan output controller. |
| EDC Bank Reader | Bridge terpisah, TCP. Di luar cakupan tim lapangan. |
| LPR Service (kamera) | gRPC internal + RTSP/ONVIF ke kamera. |
| Scanner QR keluar | Dibaca **PC Kasir**, bukan controller (USB-HID/serial, H4). |

Controller A6/A9 **tidak mencetak tiket** dan **tidak membaca QR**. Jangan menyambungkan printer atau
scanner ke output controller.

---

## 2. Transport & Jaringan (PRD v3 §5.1)

- **Controller = TCP server.** Edge (PC Admin lahan) = **client**.
- Satu **koneksi TCP persisten** per controller. Edge yang membuka dan menjaga koneksi.
- Alamat per gerbang disimpan di `gates.endpoint` berformat `ip:port`. **Port default `56001`.**
- Semua komunikasi lewat **LAN lokal lahan**. Controller tidak perlu — dan tidak boleh — terekspos
  ke internet publik.

**Yang kami butuhkan dari tim lapangan:** IP **statis** untuk tiap controller, dicatat per gerbang.
Lihat item terbuka **H5** (§9) sebelum menetapkan peta jaringan.

---

## 3. Format Frame (PRD v3 §5.2)

```
┌────────────┬──────────────────┬──────────────────┬────────────┐
│  Header    │    Command       │   Data (opsional)│  Footer    │
│   0xA6     │     ASCII        │      ASCII       │   0xA9     │
└────────────┴──────────────────┴──────────────────┴────────────┘
```

- **Tanpa** LEN, **tanpa** CRC, **tanpa** address, **tanpa** byte-stuffing.
- Byte kontrol `0xA6`/`0xA9` bernilai > 127; muatan ASCII < 128 → tidak mungkin bentrok, sehingga
  stuffing tak diperlukan.
- Integritas mengandalkan **TCP**. Tidak ada checksum di lapisan aplikasi.
- Frame dianggap valid **hanya bila Header dan Footer lengkap**. Frame tak lengkap dibuang.

Edge mengakumulasi byte masuk dan memotong per pasangan `0xA6 … 0xA9`; potongan yang tak lengkap
diabaikan sampai Footer tiba (PRD v3 §6.1).

---

## 4. Perintah Host → Controller (PRD v3 §5.3)

Setiap perintah dibalas gema perintah + `OK`, mis. `OUT1ON` → `OUT1ONOK`.

| Perintah | Arti | Balasan |
|----------|------|---------|
| `OUT1ON` … `OUT4ON` | Relay output 1–4 **ON** (tahan) | `OUT<n>ONOK` |
| `OUT1OFF` … `OUT4OFF` | Relay output 1–4 **OFF** | `OUT<n>OFFOK` |
| `TRIG1` … `TRIG4` | Relay ON **1 detik** lalu OFF sendiri (pulsa) | `TRIG<n>OK` |
| `PING` | Keepalive | `PINGOK` |
| `STAT` | Baca status semua kanal | `STATabcdefgh` (§5) |

Edge mengirim `PING` **tiap 1 detik** dan menandai controller `OFFLINE` setelah **3× tanpa `PINGOK`**
(≈3 detik). Perintah yang tak dibalas di-retry **3×** sebelum dianggap gagal (PRD v3 §6.1).

---

## 5. Event Tak Diminta Controller → Host (PRD v3 §5.4)

Controller mengirim sendiri, tanpa polling.

| Event | Arti |
|-------|------|
| `IN1ON` / `IN1OFF` … `IN4ON` / `IN4OFF` | Perubahan input 1–4 (loop detector / tombol) |
| `Wxxxxxx` | Wiegand RFID — `xxxxxx` 6 hex (W-26) atau 8 hex (W-34), auto-deteksi |

> **Debounce:** kami **berasumsi input controller belum ter-debounce**. Edge melakukan debounce
> **≥150 ms** sendiri (PRD v3 §6.1). Tim lapangan **tidak perlu** menyetel debounce di controller.
> Bila controller ternyata sudah men-debounce, beri tahu kami — angka di sisi Edge akan disesuaikan.

### 5.1 STAT — **PROVISIONAL (H2)**

Format yang kami asumsikan:

```
STATabcdefgh
     ││││││││
     ││││└┴┴┴── e–h : Output 1–4  ('1' = ON,  '0' = OFF)
     └┴┴┴────── a–d : Input  1–4  ('1' = HIGH,'0' = LOW)
```

⚠️ **Semantik STAT belum dikonfirmasi vendor** — klien menyatakan "format STAT belum pasti"
(PRD v3 §5.5, item terbuka H2). Edge memakai `STAT` untuk **rekonstruksi status saat reconnect**,
jadi salah tafsir di sini berakibat status gerbang salah setelah koneksi pulih. **Wajib
dikonfirmasi sebelum dokumen ini difinalkan.**

---

## 6. Peta Pin per Gerbang (KEPUTUSAN DESAIN v3)

Ini bagian yang paling penting untuk **wiring lapangan**. Nilai di bawah adalah **default** dan dapat
dikonfigurasi per gerbang lewat `gates.config` — tetapi kabel harus dipasang sesuai tabel ini kecuali
disepakati lain tertulis.

### 6.1 Gerbang MASUK (1 controller)

| Kanal | Fungsi |
|-------|--------|
| `IN1` | **LD1** — loop kehadiran sebelum palang (trigger LPR + aktifkan tombol/RFID) |
| `IN2` | **LD2** — loop di bawah palang (interlock + konfirmasi kendaraan lewat) |
| `IN3` | Tombol ambil tiket |
| `IN4` | Cadangan (sensor tamper / pintu box) |
| `Wiegand` | RFID reader member |
| `OUT1` | Palang (naik/turun) |
| `OUT2` | Lampu hijau |
| `OUT3` | Lampu merah |
| `OUT4` | Buzzer / cadangan |

### 6.2 Gerbang KELUAR (1 controller)

| Kanal | Fungsi |
|-------|--------|
| `IN1` | **LD3** — loop kehadiran sebelum palang keluar |
| `IN2` | **LD4** — loop di bawah palang keluar (interlock + lewat) |
| `IN3` | Cadangan |
| `IN4` | Cadangan |
| `Wiegand` | RFID reader member (tap keluar) |
| `OUT1` | Palang keluar |
| `OUT2` | Lampu hijau |
| `OUT3` | Lampu merah |
| `OUT4` | Cadangan |

**Kritis:** `IN2` **harus** loop di bawah palang pada kedua gerbang. Seluruh interlock keselamatan
(§7) bergantung pada kanal ini. Salah pasang `IN1`↔`IN2` berarti palang dapat menutup di atas
kendaraan.

---

## 7. Apa yang Ditegakkan **Edge** (bukan controller)

> **Perubahan besar dari kontrak v1.0.** Pada v1.0 kami mewajibkan firmware menegakkan interlock,
> fail-safe, debounce dan auto-close. Controller A6/A9 adalah perangkat I/O sederhana yang **tidak
> menyediakan fungsi tersebut**, sehingga seluruhnya **pindah ke Edge** (PRD v3 §6.2, prinsip P9).
>
> **Tim lapangan tidak perlu memprogram apa pun di controller.** Controller cukup meneruskan input
> dan menjalankan perintah relay.

Yang dijamin Edge:

1. **Interlock keselamatan (P4).** Edge **tidak akan** mengirim `OUT1OFF` (palang turun) selama
   `IN2` (LD2/LD4) = HIGH. Palang menunggu sampai kendaraan keluar dari loop bawah (falling edge).
2. **Tutup dipicu sensor.** Palang dibuka oleh perintah (`OUT1ON`) saat otorisasi, dan ditutup
   **hanya setelah** pola `IN2` rising → falling terdeteksi.
3. **Auto-close pengaman.** Palang terbuka > **60 detik** dengan loop bawah LOW → Edge menutup.
4. **Alarm.** `IN2` HIGH > **120 detik** → `BARRIER_BLOCKED`. Loop naik tanpa otorisasi →
   `UNAUTHORIZED_PASSAGE`.
5. **Debounce ≥150 ms** atas seluruh event `INxON/OFF`.
6. **Deteksi OFFLINE** via `PING`/`PINGOK`, reconnect exponential backoff + jitter
   (0.5s → 1s → 2s → 5s → maks 15s), dan **resync `STAT`** saat pulih.
7. **Anti-tailgating.** Bila saat menutup `IN1` masih HIGH → siklus baru untuk kendaraan kedua.

**Konsekuensi keselamatan yang harus dipahami tim lapangan:** karena interlock ada di Edge, **palang
tidak punya proteksi mandiri bila PC Admin mati atau LAN putus.** Pengaman mekanis/elektris palang
(mis. sensor tekan atau auto-reverse bawaan palang) tetap **wajib** dipasang dan tidak boleh
dilepas. Konfirmasikan ke kami pengaman bawaan apa yang tersedia pada unit palang terpasang.

---

## 8. Mode Palang — **PROVISIONAL (H3)**

Dua mode didukung, dikonfigurasi per gerbang di `gates.config.barrier_mode`:

| Mode | Perintah | Untuk palang tipe |
|------|----------|-------------------|
| **`hold`** (default) | `OUT1ON` buka, `OUT1OFF` tutup | Relay ditahan; Edge kontrol penuh timing tutup lewat sensor |
| `pulse` | `TRIG1` (pulsa 1 dtk) | "pulse-to-open" dengan auto-close mekanis bawaan palang |

⚠️ **Belum diputuskan** mode mana yang dipakai di lahan ini — bergantung tipe palang terpasang
(item terbuka H3). Tim lapangan **wajib melaporkan**: apakah palang auto-close secara mekanis, dan
apakah ia menerima sinyal tahan (hold) atau pulsa.

---

## 9. Item Terbuka — Harus Ditutup Sebelum Versi Final

| # | Pertanyaan | Dampak bila salah |
|---|-----------|-------------------|
| **H2** | Semantik `STAT` — urutan & arti karakter; apakah lebih kaya dari 8 kanal? | Status gerbang salah setelah reconnect |
| **H3** | Tipe palang: `hold` atau `pulse`? Ada auto-close mekanis? | Palang tak menutup, atau menutup dua kali |
| **H5** | Benarkah 1 controller = 1 gerbang? IP tiap controller statis? | Peta jaringan & jumlah unit salah |

Sampai ketiganya tertutup, dokumen ini **tetap berstatus DRAFT**.

---

## 10. Kontrak v1.0 (STX/CRC16 di atas RS232) — DIBATALKAN

Versi 1.0 dokumen ini menetapkan protokol rancangan sendiri: `STX | ADDR | CMD | LEN | PAYLOAD |
CRC16 | ETX` dengan byte-stuffing, di atas RS232 9600 8N1, dan mewajibkan firmware menegakkan
interlock/heartbeat/debounce/auto-close.

Kontrak itu **tidak lagi berlaku**. Hardware nyata sudah memiliki protokol vendor sendiri (A6/A9-TCP),
sehingga tidak ada firmware kustom yang perlu ditulis. Rinciannya tetap dapat dibaca di:

- `docs/PRD_PONDASI.md` §5.3 (spesifikasi v2 lengkap),
- `docs/CHANGES_v2_to_v3.md` §B baris 2–4 (alasan perubahan),
- `services/edge-api/internal/hardware/protocol/` (implementasi codec v2, **dipertahankan sebagai
  referensi saja** — tidak dipakai kode produksi),
- riwayat git dokumen ini.

**Jangan mengimplementasikan kontrak v1.0.**

---

## 11. Simulator & Rujukan

- Interface Layer-2 (`Barrier`, `LoopDetector`, `IndicatorLight`, `RFIDReader`) — tidak berubah dari
  v2: `services/edge-api/internal/hardware/device.go`.
- Simulator perangkat generik (prinsip P7): `services/edge-api/internal/hardware/sim/`.
- Driver A6/A9 (`internal/hardware/tcpctl`) — **sebagian ada** (task 1.1–1.5: parser framing,
  reconnect backoff, keepalive `PING`/`PINGOK`, korelasi perintah↔respons, debounce input),
  diuji terhadap server TCP palsu di dalam test. **Belum** tersambung ke `gatesvc`: parser
  Wiegand (1.6) dan implementasi interface Layer-2 (1.7) masih terbuka, sehingga transport
  `tcp` belum dapat dipakai end-to-end. Lihat `docs/TASKS.md` EPIK 1.
