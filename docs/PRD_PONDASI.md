# PRD PONDASI — SMART GATE & PARKING MANAGEMENT SYSTEM

**Versi:** 2.0.0 (Foundation / Underlying Design)
**Menggantikan:** PRD 1.0.0 (`Jabar-Creative/Bangun-Parkir-Mandiri/PRD.md`)
**Basis scope:** `New_Scope_Parking_System_Revisi.docx` (Konsolidasi Core MVP + New Scope — Revisi Anggaran)
**Basis UI:** prototype `apps/mvp-demo` (React + TS, mock Zustand store)
**Tanggal dokumen:** 22 Juli 2026
**Durasi development:** 4 minggu (maksimum 1 bulan)
**Kapasitas tim:** 2 developer fullstack

---

## DAFTAR ISI

| § | Bagian |
|---|--------|
| 1 | Tujuan Dokumen & Batas Tanggung Jawab |
| 2 | Konsolidasi Scope & Traceability |
| 3 | Prinsip Arsitektur (Pondasi) |
| 4 | Arsitektur Sistem |
| 5 | Desain Logika Controller & Interaksi Perangkat |
| 6 | Modul Pembayaran |
| 7 | LPR / OCR & Log OCR |
| 8 | Membership, Anti-Passback & Auto-Expiration |
| 9 | Immutable Audit Log |
| 10 | Sinkronisasi Edge → Cloud |
| 11 | Model Data (PostgreSQL) |
| 12 | Spesifikasi Dashboard |
| 13 | API Surface |
| 14 | Keamanan & RBAC |
| 15 | Non-Functional Requirements |
| 16 | Timeline 4 Minggu |
| 17 | Scope yang Diturunkan ke Fase 2 |
| 18 | Risiko, Asumsi & Open Questions |
| 19 | Definition of Done |

---

## 1. TUJUAN DOKUMEN & BATAS TANGGUNG JAWAB

### 1.1 Tujuan

Dokumen ini adalah **source of truth pondasi** untuk keseluruhan sistem parkir. Ia mendefinisikan
arsitektur, logika, kontrak antarmuka, model data, dan urutan pengerjaan — pada level yang cukup
detail sehingga developer dapat langsung mengimplementasikan tanpa perlu menebak keputusan desain.

Dokumen ini menggantikan PRD 1.0.0 karena tiga alasan:

1. **Scope berubah.** Dokumen revisi memindahkan modul pembayaran dari Core MVP ke New Scope,
   dan menambah JTMO/e-Toll via EDC sebagai kebutuhan bisnis utama hasil survey lapangan.
2. **Timeline berubah.** Dari 7 minggu menjadi 4 minggu.
3. **Batas tanggung jawab berubah.** Pemrograman mikrokontroler di lapangan ditangani pihak lain.

### 1.2 Batas Tanggung Jawab — YANG MENJADI TUGAS KITA

| Area | Deliverable kita |
|------|------------------|
| **Logika controller** | State machine gerbang masuk & keluar, aturan keputusan, timing, penanganan kegagalan, interlock keselamatan |
| **Kontrak interaksi perangkat** | Spesifikasi protokol serial/TCP yang WAJIB diimplementasikan controller lapangan (§5.3) |
| **Hardware bridge (sisi PC)** | Driver Go di Edge Node: codec frame, port manager, heartbeat, retry, telemetry |
| **Device simulator** | Simulator perangkat lengkap agar seluruh logika dapat dites tanpa perangkat fisik |
| **Backend Edge & Cloud** | Seluruh logika transaksional, API, sinkronisasi, audit |
| **Dashboard & POS** | Seluruh antarmuka web |
| **Database** | Skema, migrasi, kebijakan retensi |
| **Integrasi payment** | Adapter EDC, QRIS, e-wallet |

### 1.3 Batas Tanggung Jawab — YANG BUKAN TUGAS KITA

| Area | Ditangani oleh | Ketergantungan kita |
|------|----------------|---------------------|
| **Firmware mikrokontroler** | Tim lapangan | Mereka harus mengimplementasikan protokol di §5.3 |
| **Wiring & instalasi fisik** | Tim lapangan | Peta port & alamat perangkat (§5.1) harus disepakati |
| **Kalibrasi loop detector** | Tim lapangan | Kita hanya konsumsi sinyal HIGH/LOW yang sudah ter-debounce di sisi mereka |
| **Pengadaan & sertifikasi EDC** | Vendor/bank acquirer | Kita butuh SDK + dokumen protokol + unit tes (§18) |
| **Pemasangan kamera & pencahayaan** | Tim lapangan | Kualitas gambar menentukan akurasi LPR |

> **Konsekuensi penting:** Item scope *"Pemrograman mikrokontroler (RS232/TCP-IP) untuk kontrol palang"*
> pada dokumen revisi kita tafsirkan ulang sebagai: **kita mendefinisikan protokol dan membangun
> driver sisi PC + simulator; tim lapangan mengimplementasikan sisi mikrokontroler sesuai kontrak
> tersebut.** Ini perlu dikonfirmasi tertulis ke klien agar tidak ada gap ekspektasi.

---

## 2. KONSOLIDASI SCOPE & TRACEABILITY

### 2.1 Perubahan Material dari PRD 1.0.0

| # | Perubahan | Dampak arsitektur |
|---|-----------|-------------------|
| 1 | **Modul Keluar & POS Cashless dipangkas** menjadi hanya *"Antarmuka Kasir (POS) responsif dengan komparasi foto kendaraan"* | QRIS & EDC keluar dari Core MVP |
| 2 | **JTMO/e-Toll via EDC masuk New Scope** (Rp12jt — item terbesar) | Menjadi jalur pembayaran utama, bukan pelengkap |
| 3 | **Perluasan PG ke e-wallet + real-time** (Rp5jt) | Satu paket vendor API dengan JTMO |
| 4 | **Migrasi mock store → PostgreSQL** (Rp3,5jt) | Konfirmasi bahwa state sekarang masih Zustand mock |
| 5 | **Modifikasi Log OCR** (Rp2,5jt) | Tabel `ocr_logs` terpisah dengan koreksi manual |
| 6 | **CRON Auto-Expiration** (Rp1jt) | Scheduler terpisah, bukan cek inline |
| 7 | **Refine Dashboard diperluas** | + bulk CSV import/export, + visualisasi grafis, + multi-tenant di semua flow |
| 8 | **QA dipangkas** dari 2 fase (Rp10jt) → 1 fase (Rp5jt) | Buffer testing menipis — risiko utama, lihat §18 |
| 9 | **Redis & PostgreSQL terkluster hilang dari anggaran** | OpEx hanya 1 managed PG node, tanpa Redis (§4.3) |

### 2.2 Matriks Traceability

Setiap requirement di dokumen ini punya tag `FR-x.y` / `NFR-x`. Tag dipertahankan dari PRD 1.0.0
agar kerja lama tetap tertelusur, dengan tambahan `FR-6.x` (Refine Dashboard) dan `FR-7.x` (New Scope).

| Modul | Tag | Sumber | Fase |
|-------|-----|--------|------|
| Arsitektur Hybrid & API Gateway | `FR-1.1` … `FR-1.4` | Core MVP #1 | 1 |
| Pintu Masuk & LPR Dasar | `FR-2.1` … `FR-2.4` | Core MVP #2 | 1 |
| Keluar & POS | `FR-3.1` | Core MVP #3 (dipangkas) | 1 |
| Hardware Bridge Interfacing | `FR-4.1` … `FR-4.3` | Core MVP #4 | 1 |
| Membership & Audit Log | `FR-5.1` … `FR-5.3` | Core MVP #5 | 1 |
| Refine Dashboard | `FR-6.1` … `FR-6.4` | Core MVP #6 | 1 |
| JTMO / e-Toll via EDC | `FR-7.1` … `FR-7.3` | New Scope | 1b |
| E-Wallet & PG real-time | `FR-7.4` … `FR-7.5` | New Scope | 1 |
| Migrasi PostgreSQL | `FR-7.6` | New Scope | 1 |
| Log OCR | `FR-7.7` | New Scope | 1 |
| CRON Auto-Expiration | `FR-7.8` | New Scope | 1 |

### 2.3 Catatan Anggaran — Perlu Klarifikasi

Dokumen revisi §4 menyatakan:

```
Core MVP Stage        Rp30.000.000
New Scope — Fase 1    Rp29.000.000
TOTAL KESELURUHAN     Rp54.000.000
```

**Rp30.000.000 + Rp29.000.000 = Rp59.000.000, bukan Rp54.000.000.** Selisih Rp5.000.000.

Rincian New Scope sendiri konsisten (12 + 5 + 3,5 + 2,5 + 1 + 5 = 29). Kemungkinan: ada diskon
Rp5jt yang belum tertulis, atau angka total belum di-update setelah QA disesuaikan dari Rp10jt ke
Rp5jt. **Harus diklarifikasi sebelum dokumen dikirim ke klien** — ini angka yang akan dijadikan
dasar kontrak.

---

## 3. PRINSIP ARSITEKTUR (PONDASI)

Tujuh prinsip berikut mengikat seluruh keputusan teknis. Jika sebuah implementasi melanggar salah
satunya, implementasi itu salah — bukan prinsipnya.

**P1 — Gerbang harus tetap hidup tanpa internet.**
Jalur masuk, keluar, dan pembayaran offline-capable tidak boleh memanggil Cloud secara sinkron.
Cloud adalah tujuan replikasi, bukan dependensi runtime.

**P2 — Ketersediaan menang atas kesempurnaan data.**
Jika LPR gagal, kendaraan tetap boleh masuk dengan plat ditandai `UNREAD`. Antrean di gerbang adalah
kegagalan bisnis yang lebih mahal daripada satu baris data yang tidak lengkap.

**P3 — Perangkat keras tidak dipercaya.**
Setiap perintah ke perangkat punya timeout, retry terbatas, dan jalur degradasi. Ketiadaan respons
adalah kondisi normal yang harus ditangani, bukan exception.

**P4 — Keselamatan fisik adalah interlock keras.**
Palang tidak pernah menutup saat loop detector di bawah palang bernilai HIGH. Aturan ini
di-*enforce* di dua tempat: logika Edge dan firmware controller. Redundan dengan sengaja.

**P5 — Uang dan audit bersifat append-only.**
Transaksi dan audit log tidak pernah di-UPDATE atau DELETE. Koreksi dilakukan dengan entri
kompensasi (`VOID`, `ADJUSTMENT`) yang juga tercatat.

**P6 — Multi-tenant sejak baris pertama.**
Setiap tabel operasional membawa `tenant_id` dan `site_id` sejak awal, walaupun deployment pertama
hanya satu lokasi. Menambahkannya belakangan berarti migrasi data yang menyakitkan.

**P7 — Semua perangkat punya simulator.**
Tidak ada satupun logika yang hanya bisa dites di lapangan. Ini bukan kemewahan — dengan timeline
4 minggu dan perangkat fisik yang belum tentu tersedia, simulator adalah jalur kritis.

---

## 4. ARSITEKTUR SISTEM

### 4.1 Topologi

```
┌──────────────────────────── LOKASI FISIK (SITE) ────────────────────────────┐
│                                                                              │
│   ┌─────────────┐   ┌─────────────┐        ┌──────────────────────────┐     │
│   │ GERBANG     │   │ GERBANG     │        │      EDGE NODE (PC)      │     │
│   │ MASUK       │   │ KELUAR      │        │                          │     │
│   │             │   │             │        │  ┌────────────────────┐  │     │
│   │ • LD1, LD2  │   │ • LD3, LD4  │        │  │ edge-api (Go)      │  │     │
│   │ • Palang    │◄──┤ • Palang    │◄───────┼──┤  ├ Gate Controller │  │     │
│   │ • Printer   │   │ • Lampu     │ RS232  │  │  ├ Fare Engine     │  │     │
│   │ • RFID      │   │ • EDC       │ TCP/IP │  │  ├ Payment Adapter │  │     │
│   │ • Lampu     │   │ • Kamera    │        │  │  ├ Audit Chain     │  │     │
│   │ • Kamera    │   │             │        │  │  └ Sync Agent      │  │     │
│   └─────────────┘   └─────────────┘        │  └────────┬───────────┘  │     │
│                                             │           │              │     │
│                                             │  ┌────────┴───────────┐  │     │
│                                             │  │ PostgreSQL lokal   │  │     │
│                                             │  │ + outbox queue     │  │     │
│                                             │  └────────────────────┘  │     │
│                                             │  ┌────────────────────┐  │     │
│                                             │  │ lpr-svc (Python)   │  │     │
│                                             │  │ gRPC :50051        │  │     │
│                                             │  └────────────────────┘  │     │
│                                             │  ┌────────────────────┐  │     │
│                                             │  │ POS Kasir (React)  │  │     │
│                                             │  │ localhost, WS      │  │     │
│                                             │  └────────────────────┘  │     │
│                                             └───────────┬──────────────┘     │
└─────────────────────────────────────────────────────────┼────────────────────┘
                                                          │
                                          Cloudflare Tunnel (outbound only)
                                                          │
┌─────────────────────────────────────────────────────────┼────────────────────┐
│                          CLOUD (DigitalOcean)           ▼                     │
│   ┌──────────────────────┐   ┌───────────────────┐   ┌──────────────────┐    │
│   │ cloud-api (Go)       │   │ Managed PostgreSQL│   │ Spaces (S3)      │    │
│   │  ├ Sync Receiver     │◄─►│ 2GB, single node  │   │ 250GB — snapshot │    │
│   │  ├ Aggregation       │   └───────────────────┘   └──────────────────┘    │
│   │  ├ Audit Verifier    │                                                    │
│   │  └ Dashboard API     │   ┌───────────────────┐                            │
│   └──────────┬───────────┘   │ Payment Gateway   │                            │
│              │                │ Midtrans/Xendit   │  (webhook masuk via CF)   │
│   ┌──────────┴───────────┐   └───────────────────┘                            │
│   │ Dashboard Web (React)│                                                     │
│   └──────────────────────┘                                                     │
└────────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 Komponen & Tanggung Jawab

| Komponen | Bahasa/Stack | Tanggung jawab | Boleh mati? |
|----------|--------------|----------------|-------------|
| `edge-api` | Go + Fiber | Seluruh logika transaksi, state machine gerbang, fare engine, audit chain, WS server untuk POS | **Tidak** — gerbang berhenti |
| `edge-db` | PostgreSQL 16 | Data transaksi lokal + outbox | **Tidak** |
| `lpr-svc` | Python + gRPC | Deteksi kendaraan (YOLOv8) + OCR plat | **Ya** — degradasi ke `UNREAD` |
| `pos-web` | React + Vite | Antarmuka kasir gerbang keluar | Ya — ada mode manual |
| `sync-agent` | Go (goroutine dalam `edge-api`) | Kirim outbox ke Cloud | **Ya** — antrean menumpuk, tidak hilang |
| `cloud-api` | Go + Fiber | Terima sync, agregasi, serve dashboard, verifikasi audit chain | Ya — Edge tidak terpengaruh |
| `cloud-db` | Managed PostgreSQL | Data agregat multi-tenant | Ya |
| `dashboard-web` | React + Vite | Dashboard pusat | Ya |

### 4.3 Penyesuaian Arsitektur terhadap Anggaran OpEx

OpEx pada dokumen revisi (~Rp2.759.000/bulan) memaksa tiga penyimpangan dari PRD 1.0.0. Ini bukan
kompromi diam-diam — konsekuensinya harus diketahui klien.

| PRD 1.0.0 | Realita anggaran | Konsekuensi & mitigasi |
|-----------|------------------|------------------------|
| Redis untuk caching & rate limiting | **Tidak ada di anggaran** | Cache in-process (`ristretto`) di `cloud-api`; rate limit token-bucket in-memory. Konsekuensi: cache tidak terbagi antar instance → **cloud-api harus single instance** sampai Redis dianggarkan |
| PostgreSQL terkluster | 1 node managed, 2GB RAM | Tidak ada HA di Cloud. Mitigasi: daily automated backup + PITR dari DigitalOcean; **Edge tetap jalan saat Cloud down (P1)**, jadi dampaknya terbatas ke dashboard |
| — | Spaces 250GB | Snapshot LPR butuh kebijakan retensi (§4.4) |

**Kapasitas Cloud (2 vCPU / 4GB):** cukup untuk ±20 site dan ±50 pengguna dashboard konkuren dengan
beban sync batch. Di atas itu perlu upgrade droplet.

### 4.4 Kebijakan Retensi Gambar

Snapshot LPR adalah konsumen storage terbesar. Perhitungan:

| Volume transaksi/hari | Ukuran/hari (2 foto × ~150KB) | 250GB habis dalam |
|-----------------------|-------------------------------|-------------------|
| 500 | ~150 MB | ~4,5 tahun |
| 2.000 | ~600 MB | ~14 bulan |
| 5.000 | ~1,5 GB | ~5,5 bulan |

**Kebijakan yang ditetapkan:**

- Foto disimpan di Edge (disk lokal) **7 hari** untuk kebutuhan komparasi & sengketa langsung.
- Upload ke Spaces terjadi **saat online**, asinkron, prioritas lebih rendah dari data transaksi.
- Di Spaces: **retensi 90 hari** untuk transaksi normal, **retensi 2 tahun** untuk transaksi
  bertanda `VOID`, `DISPUTE`, atau terkait alert `critical`.
- Lifecycle rule Spaces menghapus objek melewati retensi secara otomatis.
- Baris `vehicles_log` **tidak pernah dihapus** — hanya `img_*_key` yang menjadi null setelah purge.

### 4.5 Konektivitas Edge (Cloudflare Zero Trust Tunnel)

Anggaran mencantumkan Cloudflare Zero Trust Tunnels (gratis). Ini memberi tiga hal sekaligus:

1. **Edge → Cloud tanpa IP publik.** Edge Node di balik NAT/4G tetap bisa sync. Tidak perlu
   port-forwarding, tidak perlu IP statis — menghemat biaya langganan internet bisnis.
2. **Akses admin ke Edge Node** untuk troubleshooting jarak jauh, terproteksi identity Cloudflare
   Access. Ini menggantikan kebutuhan VPN.
3. **Webhook payment gateway** masuk melalui Cloudflare ke `cloud-api`, dengan WAF di depannya.

**Aturan:** tunnel hanya boleh membuka jalur ke `cloud-api` dan ke port admin Edge. Tunnel **tidak
boleh** menjadi jalur runtime pembayaran atau kontrol palang (melanggar P1).

---

## 5. DESAIN LOGIKA CONTROLLER & INTERAKSI PERANGKAT

Ini adalah inti dokumen. Bagian ini mendefinisikan **apa yang harus diimplementasikan tim lapangan**
dan **apa yang kita implementasikan di sisi PC**.

### 5.1 Inventaris Perangkat & Peta Port

Diselaraskan dengan `HardwareBridgeSim.tsx` pada prototype.

| Kanal | Transport | Perangkat | Arah | Alamat |
|-------|-----------|-----------|------|--------|
| `COM3` | RS232, 9600 8N1 | Controller Gerbang Masuk: LD1, LD2, palang, lampu, RFID Mifare, printer dispenser | Bi-directional | `0x01` |
| `COM4` | RS232, 9600 8N1 | Controller Gerbang Keluar: LD3, LD4, palang, lampu | Bi-directional | `0x02` |
| `TCP :5001` | TCP/IP | EDC Bank Reader Bridge | Bi-directional | — |
| `gRPC :50051` | gRPC | LPR Service | Request/Response | — |
| `RTSP` | RTSP/ONVIF | IP Camera masuk & keluar | Pull | per-gate config |

**Peta sinyal loop detector:**

| Sinyal | Posisi fisik | Fungsi logis |
|--------|--------------|--------------|
| `LD1` | Sebelum palang masuk | Deteksi kehadiran kendaraan → trigger LPR & aktivasi tombol |
| `LD2` | Setelah palang masuk | Konfirmasi kendaraan lewat → izin tutup palang + **interlock keselamatan** |
| `LD3` | Sebelum palang keluar | Deteksi kendaraan → trigger identifikasi & POS |
| `LD4` | Setelah palang keluar | Konfirmasi lewat → izin tutup + interlock |

**Peta lampu indikator** (`FR-4.3`):

| Warna | Arti | Kondisi |
|-------|------|---------|
| Merah solid | Stop / sedang diproses | State `IDLE`, `CAPTURING`, `AWAITING_*` |
| Hijau solid | Silakan jalan | State `OPENING`, `OPEN`, `CLEARING` |
| Kuning berkedip 1Hz | Peringatan | Kertas menipis, LPR down, mode offline |
| Merah berkedip 2Hz | Error / gerbang terkunci | Kertas habis, controller offline, state `FAULT` |

### 5.2 Model Abstraksi Perangkat

`edge-api` tidak pernah menulis byte langsung dari logika bisnis. Tiga lapis:

```
┌──────────────────────────────────────────────┐
│ Layer 3 — Gate Controller (state machine)    │  Bahasa: domain
│   "izinkan kendaraan masuk"                  │
├──────────────────────────────────────────────┤
│ Layer 2 — Device Abstraction (interface Go)  │  Bahasa: perangkat
│   Barrier.Open() / Printer.Print(payload)    │
├──────────────────────────────────────────────┤
│ Layer 1 — Transport Driver (codec + port)    │  Bahasa: byte
│   frame encode/decode, CRC, retry, heartbeat │
└──────────────────────────────────────────────┘
```

Interface Layer 2 (`FR-4.1`):

```go
type Barrier interface {
    Open(ctx context.Context) error
    Close(ctx context.Context) error
    State(ctx context.Context) (BarrierState, error)  // OPEN|CLOSED|MOVING|UNKNOWN
}

type LoopDetector interface {
    Read(ctx context.Context) (bool, error)
    Subscribe() <-chan LoopEvent                       // event unsolicited dari controller
}

type IndicatorLight interface {
    Set(ctx context.Context, pattern LightPattern) error
}

type TicketPrinter interface {
    Print(ctx context.Context, t TicketPayload) error
    Status(ctx context.Context) (PrinterStatus, error) // OK|PAPER_LOW|PAPER_OUT|JAM|OFFLINE
    Subscribe() <-chan PrinterEvent                    // termasuk TICKET_TAKEN
}

type RFIDReader interface {
    Subscribe() <-chan RFIDTap                         // {UID, ReadAt}
}

type PaymentTerminal interface {
    Charge(ctx context.Context, amount int64, method CardType) (ChargeResult, error)
    Cancel(ctx context.Context, ref string) error
    Status(ctx context.Context) (TerminalStatus, error)
}
```

Setiap interface punya **dua implementasi**: `serial`/`tcp` (produksi) dan `sim` (simulator, P7).
Pemilihan lewat konfigurasi, bukan build tag — supaya bisa diganti saat runtime untuk demo.

### 5.3 KONTRAK PROTOKOL — Spesifikasi untuk Tim Lapangan

> **Bagian ini adalah deliverable formal ke tim controller lapangan.** Selama sisi mereka mematuhi
> kontrak ini, kedua sisi dapat dikembangkan paralel tanpa saling menunggu.

#### 5.3.1 Format Frame

```
┌─────┬──────┬─────┬─────┬───────────┬────────┬─────┐
│ STX │ ADDR │ CMD │ LEN │  PAYLOAD  │ CRC16  │ ETX │
│0x02 │  1B  │ 1B  │ 1B  │  0–255 B  │  2B LE │0x03 │
└─────┴──────┴─────┴─────┴───────────┴────────┴─────┘
```

- `ADDR` — `0x01` controller masuk, `0x02` controller keluar, `0xFF` broadcast
- `CRC16` — CRC-16/MODBUS, dihitung atas `ADDR..PAYLOAD`
- Byte `0x02`, `0x03`, `0x10` di dalam payload di-*escape* dengan `0x10` + (byte XOR `0x20`)

#### 5.3.2 Tabel Perintah (PC → Controller)

| CMD | Nama | Payload | Respons | Timeout |
|-----|------|---------|---------|---------|
| `0x01` | `GATE_OPEN` | `[durasi_pulse_ms:2B]` | `ACK` + `GATE_STATE` | 500 ms |
| `0x02` | `GATE_CLOSE` | — | `ACK` + `GATE_STATE` | 500 ms |
| `0x03` | `GATE_QUERY` | — | `GATE_STATE` | 300 ms |
| `0x10` | `LIGHT_SET` | `[pola:1B]` `00`=off `01`=merah `02`=hijau `03`=kuning-blink `04`=merah-blink | `ACK` | 300 ms |
| `0x20` | `LOOP_QUERY` | `[loop_id:1B]` | `LOOP_STATE` | 300 ms |
| `0x30` | `PRINT_TICKET` | `[len:2B][data ESC/POS]` | `ACK` lalu `PRINT_DONE` | 300 ms / 2000 ms |
| `0x31` | `PRINTER_QUERY` | — | `PRINTER_STATE` | 300 ms |
| `0x32` | `TICKET_RETRACT` | — | `ACK` | 500 ms |
| `0xF0` | `HEARTBEAT` | `[epoch_ms:8B]` | `HEARTBEAT_ACK` | 500 ms |
| `0xF1` | `RESET` | — | `ACK` | 2000 ms |

#### 5.3.3 Event Tak Diminta (Controller → PC)

Controller **mengirim sendiri** tanpa diminta. Ini wajib — polling tidak akan memenuhi budget latensi.

| CMD | Nama | Payload | Kapan dikirim |
|-----|------|---------|---------------|
| `0x21` | `LOOP_EVENT` | `[loop_id:1B][state:1B][ts:8B]` | Setiap perubahan state loop, **sudah ter-debounce ≥150 ms di sisi controller** |
| `0x33` | `PRINTER_EVENT` | `[event:1B]` `01`=TICKET_TAKEN `02`=PAPER_LOW `03`=PAPER_OUT `04`=JAM | Perubahan status printer |
| `0x40` | `RFID_TAP` | `[uid_len:1B][uid:nB][ts:8B]` | Kartu Mifare terbaca |
| `0x50` | `BUTTON_PRESS` | `[button_id:1B][ts:8B]` | Tombol ambil tiket ditekan |
| `0x04` | `GATE_STATE` | `[state:1B]` `00`=closed `01`=open `02`=moving `03`=fault | Perubahan posisi palang |
| `0xFE` | `FAULT` | `[kode:1B][detail:nB]` | Kondisi abnormal |

#### 5.3.4 Kewajiban Sisi Controller (Non-Negotiable)

Lima aturan berikut **wajib** diimplementasikan di firmware, tidak boleh hanya mengandalkan PC:

1. **Interlock keselamatan (P4).** Palang **DILARANG** menutup selama loop di bawah palang (LD2/LD4)
   bernilai HIGH — bahkan jika PC mengirim `GATE_CLOSE`. Controller harus menolak dan membalas
   `FAULT` kode `0x01 SAFETY_INTERLOCK`.
2. **Fail-safe kehilangan komunikasi.** Jika tidak menerima `HEARTBEAT` selama **5 detik**,
   controller masuk mode aman: palang **ditutup** (jika loop bawah LOW), lampu **merah berkedip**,
   tombol tiket **dinonaktifkan**. Controller TIDAK boleh membuka palang atas inisiatif sendiri.
3. **Debounce loop detector ≥150 ms** di sisi controller. PC menerima sinyal yang sudah bersih.
4. **Auto-close pengaman.** Jika palang terbuka > **60 detik** tanpa perintah apapun dan loop bawah
   LOW, controller menutup sendiri dan mengirim `FAULT` kode `0x02 AUTOCLOSE_TIMEOUT`.
5. **Idempotensi.** `GATE_OPEN` pada palang yang sudah terbuka harus dibalas `ACK` tanpa efek
   samping — bukan error, bukan pulse ganda.

#### 5.3.5 Kewajiban Sisi PC (Kita)

- Kirim `HEARTBEAT` setiap **1 detik**. Tandai controller `OFFLINE` setelah **3 heartbeat** tanpa
  balasan (3 detik).
- Retry perintah maksimal **3×** dengan backoff 100/200/400 ms. Setelah itu → `FAULT` + alert.
- Setiap frame TX dan RX dicatat ke ring buffer telemetry (ditampilkan di halaman Hardware Config,
  §12.11) dan ke `audit_logs` jika `severity ≥ warning`.

### 5.4 State Machine — Gerbang Masuk (`FR-2.x`, `FR-4.2`)

```
                    ┌──────────────────────────────────────┐
                    │             IDLE                     │
                    │  palang CLOSED · lampu MERAH         │◄──────────────┐
                    └───────────────┬──────────────────────┘               │
                       LD1 rising (debounced)                              │
                                    ▼                                      │
                    ┌──────────────────────────────────────┐               │
                    │        VEHICLE_PRESENT               │               │
                    │  • trigger snapshot LPR (async)      │               │
                    │  • aktifkan tombol + RFID reader     │               │
                    │  • mulai transaksi DRAFT             │               │
                    └───┬────────────────────────┬─────────┘               │
              RFID_TAP  │                        │ BUTTON_PRESS            │
                        ▼                        ▼                         │
        ┌───────────────────────┐   ┌──────────────────────────┐           │
        │   MEMBER_VALIDATION   │   │        ISSUING           │           │
        │ • cek masa aktif      │   │ • generate UUIDv7        │           │
        │ • cek anti-passback   │   │ • encode QR payload      │           │
        │ • cek kuota slot      │   │ • PRINT_TICKET → COM3    │           │
        └───┬───────────────┬───┘   └────────────┬─────────────┘           │
      tolak │           terima                   │ PRINT_DONE              │
            ▼               │                    ▼                         │
    ┌───────────────┐       │      ┌──────────────────────────┐            │
    │   REJECTED    │       │      │      AWAITING_PULL       │            │
    │ lampu blink   │       │      │  tunggu TICKET_TAKEN     │            │
    │ 3s → IDLE     │       │      │  timeout 30s → RETRACT   │            │
    └───────┬───────┘       │      └────────────┬─────────────┘            │
            │               │        TICKET_TAKEN                          │
            │               ▼                   ▼                          │
            │      ┌────────────────────────────────────────┐              │
            │      │              OPENING                   │              │
            │      │  GATE_OPEN → COM3 · lampu HIJAU        │              │
            │      │  commit transaksi IN_PREMISES          │              │
            │      └────────────────┬───────────────────────┘              │
            │             GATE_STATE=open                                  │
            │                       ▼                                      │
            │      ┌────────────────────────────────────────┐              │
            │      │                OPEN                    │              │
            │      │  tunggu LD2 rising (kendaraan lewat)   │              │
            │      │  timeout 45s → CLOSING (no-show)       │              │
            │      └────────────────┬───────────────────────┘              │
            │                LD2 rising                                    │
            │                       ▼                                      │
            │      ┌────────────────────────────────────────┐              │
            │      │              CLEARING                  │              │
            │      │  tunggu LD2 falling                    │              │
            │      │  ⚠ INTERLOCK: dilarang tutup saat HIGH │              │
            │      └────────────────┬───────────────────────┘              │
            │                LD2 falling                                   │
            │                       ▼                                      │
            │      ┌────────────────────────────────────────┐              │
            │      │              CLOSING                   │              │
            │      │  GATE_CLOSE · lampu MERAH              │──────────────┘
            │      └────────────────────────────────────────┘
            └───────────────────────────────────────────────────────────────┘

     State khusus (dapat dimasuki dari state manapun):
     ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐
     │    FAULT     │  │  MAINTENANCE │  │  LOCKED_NO_PAPER │
     │ merah blink  │  │ manual admin │  │ member-only mode │
     └──────────────┘  └──────────────┘  └──────────────────┘
```

#### 5.4.1 Aturan Anti-Tailgating (`FR-4.2`) — Presisi

Aturan yang benar bukan sekadar "tutup saat LD2 LOW". Rincian:

| Situasi | Perilaku yang benar | Alasan |
|---------|---------------------|--------|
| LD2 HIGH (mobil di bawah palang) | **Jangan pernah tutup** | Keselamatan (P4) |
| LD2 baru saja falling, LD1 LOW | Tutup segera | Kendaraan tunggal sudah lewat, normal |
| LD2 baru saja falling, **LD1 HIGH** | **Tetap tutup**, lalu mulai siklus baru untuk kendaraan kedua | Inilah anti-tailgating: mobil kedua wajib ambil tiket sendiri |
| LD1 HIGH terus > 120 detik tanpa aksi | Alert `VEHICLE_STALLED`, lampu kuning blink | Kemungkinan mobil mogok atau loop rusak |
| LD2 HIGH > 120 detik | Alert `critical` + `BARRIER_BLOCKED` | Kendaraan tersangkut, butuh intervensi |
| LD2 rising **tanpa** state OPEN | Alert `critical` `UNAUTHORIZED_PASSAGE` + foto | Palang ditembus / dibuka paksa |

#### 5.4.2 Tabel Timeout Gerbang Masuk

| Fase | Timeout | Aksi saat timeout |
|------|---------|-------------------|
| Snapshot + OCR LPR | **1.000 ms** (`NFR-1`) | Lanjut dengan `plate_in = UNREAD`, tandai `needs_review` |
| Cetak tiket sejak tombol | **1.000 ms** (`NFR-1`) | Jika gagal → `FAULT`, lampu merah blink, alert |
| Tiket menunggu diambil | 30 detik | `TICKET_RETRACT`, transaksi jadi `VOID`, log audit |
| Palang membuka | 3 detik | Retry 1×, lalu `FAULT` |
| Kendaraan lewat setelah palang buka | 45 detik | Tutup palang, transaksi tetap `IN_PREMISES`, tandai `no_show` |
| Debounce loop (sisi controller) | 150 ms | — |

#### 5.4.3 Matriks Degradasi Gerbang Masuk

| Yang rusak | Casual (tiket) | Member (RFID) | Lampu | Alert |
|------------|----------------|---------------|-------|-------|
| LPR service down | **Tetap jalan**, `plate=UNREAD` | Tetap jalan | Kuning blink | `warning` |
| Kertas menipis | Tetap jalan | Tetap jalan | Kuning blink | `warning` |
| **Kertas habis / printer jam** | **Berhenti** — state `LOCKED_NO_PAPER` | **Tetap jalan** | Merah blink | `critical` |
| Internet putus | Tetap jalan (P1) | Tetap jalan | Kuning blink | `warning` |
| Controller COM3 offline | Berhenti | Berhenti | (mati) | `critical` |
| PostgreSQL lokal down | **Berhenti total** | Berhenti | Merah blink | `critical` |
| Kamera down | Tetap jalan, tanpa foto | Tetap jalan | Kuning blink | `warning` |

> Perhatikan baris "kertas habis": member tetap bisa masuk. Ini keputusan desain sadar — memutus
> akses penghuni tetap karena printer kehabisan kertas adalah kegagalan yang tidak perlu.

### 5.5 State Machine — Gerbang Keluar (`FR-3.1`, `FR-7.x`)

```
┌────────────────────────────────────────────────────────────────────┐
│                            IDLE                                     │
│  palang CLOSED · lampu MERAH · POS menampilkan antrean kosong      │◄──┐
└──────────────────────────┬─────────────────────────────────────────┘   │
                    LD3 rising                                            │
                           ▼                                              │
┌────────────────────────────────────────────────────────────────────┐   │
│                       IDENTIFYING                                   │   │
│  • snapshot LPR keluar (async, ≤1000 ms)                            │   │
│  • aktifkan scanner QR tiket + RFID reader                          │   │
│  • POS menampilkan panel "kendaraan terdeteksi"                     │   │
└───┬──────────────┬──────────────────┬──────────────────────────────┘   │
QR  │         RFID │             tidak│ada identifikasi (15s)             │
scan│          tap │                  ▼                                  │
    │              │        ┌──────────────────────┐                     │
    │              │        │  PLATE_LOOKUP        │                     │
    │              │        │ cari via plate_in    │                     │
    │              │        │ (butuh konfirmasi    │                     │
    │              │        │  kasir)              │                     │
    │              │        └──────────┬───────────┘                     │
    ▼              ▼                   ▼                                 │
┌────────────────────────────────────────────────────────────────────┐  │
│                      TRANSACTION_FOUND                              │  │
│  POS menampilkan: waktu masuk · durasi · jenis · TARIF              │  │
│  ⚠ KOMPARASI FOTO: img_in (masuk) vs snapshot live (sekarang)       │  │
│  kasir wajib menekan "Foto Cocok" / "Tidak Cocok"                   │  │
└───┬───────────────────────────────────────┬────────────────────────┘  │
    │ cocok                       tidak cocok│                           │
    │                                        ▼                           │
    │                          ┌──────────────────────────┐              │
    │                          │      DISPUTE_HOLD        │              │
    │                          │ audit critical + eskalasi│              │
    │                          │ butuh override SuperAdmin│              │
    │                          └──────────────────────────┘              │
    ▼                                                                    │
┌────────────────────────────────────────────────────────────────────┐  │
│                       FARE_CALCULATED                               │  │
│  tarif = f(jenis, durasi, tarif dasar, peak multiplier, grace)      │  │
│  member aktif → tarif Rp0, langsung ke OPENING                      │  │
└──────────────────────────┬─────────────────────────────────────────┘  │
                           ▼                                             │
┌────────────────────────────────────────────────────────────────────┐  │
│                     AWAITING_PAYMENT                                │  │
│  kasir memilih metode → lihat §6 matriks metode                     │  │
│  timeout 180 detik → batalkan pembayaran, kembali ke FARE_CALCULATED│  │
└──────────────────────────┬─────────────────────────────────────────┘  │
                  status SETTLED                                         │
                           ▼                                             │
┌────────────────────────────────────────────────────────────────────┐  │
│                          PAID                                       │  │
│  tulis payments · vehicles_log→COMPLETED · audit · enqueue outbox    │  │
│  cetak struk (opsional) · POS update < 500 ms (NFR-1)               │  │
└──────────────────────────┬─────────────────────────────────────────┘  │
                           ▼                                             │
        OPENING → OPEN → CLEARING (LD4) → CLOSING ────────────────────────┘
                  (aturan interlock & anti-tailgating identik §5.4.1)
```

#### 5.5.1 Aturan Khusus Gerbang Keluar

| Kasus | Penanganan |
|-------|------------|
| **Tiket hilang** | Kasir pilih "Tiket Hilang" → wajib input plat manual + foto KTP/STNK → tarif = tarif maksimum harian + denda (konfigurasi per site) → audit `severity=warning` |
| **Tiket rusak/tidak terbaca** | Input `ticket_code` manual → sistem cari → jika ketemu, lanjut normal + audit `warning` |
| **Member kedaluwarsa saat di dalam** | Kendaraan tetap boleh keluar, ditagih tarif casual sejak `valid_until`, bukan sejak masuk |
| **Kendaraan tidak ada di database** | Tarif "unregistered entry" = tarif maksimum harian; audit `critical` — mengindikasikan gerbang masuk pernah ditembus |
| **Durasi < grace period** (default 15 menit) | Tarif Rp0, palang buka, transaksi `COMPLETED` amount 0 |
| **Pembayaran gagal 3× berturut** | Kembali ke `FARE_CALCULATED`, kasir tawarkan metode lain, audit `warning` |
| **Override manual buka palang** | Hanya role Kasir+ dengan alasan wajib dipilih; **selalu** audit `critical` + snapshot |

#### 5.5.2 Fare Engine

```
durasi_menit = ceil((exit_time - entry_time) / 60)

if durasi_menit <= grace_minutes:            → 0
if membership aktif dan mencakup site:       → 0

jam_terhitung  = ceil(durasi_menit / 60)
tarif_dasar    = tariffs[site][vehicle_type].base_rate
pengali        = is_peak(exit_time, peak_windows) ? peak_multiplier : 1.0

subtotal = tarif_dasar × jam_terhitung × pengali
total    = min(subtotal, max_daily_rate)     // cap harian
total    = total + denda_tiket_hilang        // jika berlaku
```

- Semua nilai uang disimpan sebagai **`BIGINT` rupiah utuh** — tidak pernah float.
- `peak_windows` adalah JSONB: `[{"days":[1,2,3,4,5],"from":"17:00","to":"21:00"}]`.
- Perhitungan tarif terjadi **di Edge**, tidak pernah di Cloud (P1).
- Setiap perubahan tarif tercatat di `audit_logs` dengan nilai lama & baru.

### 5.6 Logika Hardware Bridge — Concurrency Model

Satu port serial = satu goroutine pemilik. Tidak ada goroutine lain yang menyentuh port itu.

```
              ┌──────────────────┐
  perintah ──►│  cmdCh (buffered)│──┐
              └──────────────────┘  │
                                    ▼
                         ┌─────────────────────┐
                         │   portOwner(COM3)   │  goroutine tunggal
                         │  • serialize TX     │
                         │  • korelasi RX↔TX   │
                         │  • heartbeat ticker │
                         │  • retry & timeout  │
                         └──────────┬──────────┘
                                    │
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
          ┌──────────────────┐          ┌─────────────────────┐
          │  respCh (per-req)│          │ eventCh (unsolicited)│
          └──────────────────┘          └──────────┬──────────┘
                                                   ▼
                                        ┌─────────────────────┐
                                        │  Gate State Machine │
                                        └─────────────────────┘
```

**Aturan:**
- Perintah dan respons dikorelasikan dengan sequence number internal, bukan dengan asumsi urutan.
- Event tak diminta (`LOOP_EVENT`, `RFID_TAP`, dst.) dialirkan ke channel terpisah agar tidak
  tertukar dengan respons perintah.
- State machine gerbang berjalan di goroutine sendiri, menerima event via channel — **tidak pernah
  memblokir** pada I/O serial.
- Semua state transisi ditulis ke `gate_state_transitions` (untuk debugging) dan dipancarkan ke POS
  via WebSocket.

---

## 6. MODUL PEMBAYARAN

Ini adalah bagian dengan bobot anggaran terbesar (JTMO/e-Toll Rp12jt + e-wallet Rp5jt = Rp17jt dari
Rp29jt New Scope) dan risiko eksternal tertinggi.

### 6.1 Matriks Metode Pembayaran × Ketersediaan Offline

Ini adalah tabel terpenting di modul pembayaran. Prinsip P1 mengatakan pembayaran harus jalan tanpa
internet — tetapi itu **hanya benar untuk sebagian metode**. Perbedaan ini harus eksplisit.

| Metode | Kode | Offline? | Mekanisme | Rekonsiliasi |
|--------|------|----------|-----------|--------------|
| Tunai | `CASH` | ✅ Ya | Kasir input nominal, sistem hitung kembalian | Via Rekonsiliasi Shift (§12.6) |
| **e-Toll / e-Money via EDC** | `EDC_EMONEY` | ✅ **Ya** | EDC memotong saldo kartu secara lokal; settlement ke acquirer dilakukan EDC sendiri saat batch | **Out-of-band** — cocokkan `approval_code` + `batch_no` dengan laporan bank |
| Kartu debit/GPN via EDC | `EDC_DEBIT` | ⚠️ Sebagian | Butuh koneksi EDC ke host bank (jalur GPRS/telepon EDC sendiri, bukan internet kita) | Sama seperti di atas |
| Member RFID | `MEMBER` | ✅ Ya | Validasi lokal, tarif Rp0 atau potong kuota | Tidak ada arus kas |
| QRIS dinamis | `QRIS` | ❌ **Tidak** | Butuh call ke Midtrans/Xendit untuk mint QR + webhook settlement | Otomatis via webhook |
| E-Wallet (GoPay/OVO/DANA/ShopeePay) | `EWALLET` | ❌ **Tidak** | Sama seperti QRIS, via PG yang sama | Otomatis via webhook |

**Perilaku POS saat offline:** metode `QRIS` dan `EWALLET` **dinonaktifkan secara visual** (bukan
gagal saat dicoba) dengan tooltip "Tidak tersedia — mode offline". Ini mencegah kasir menahan
antrean karena mencoba metode yang pasti gagal.

### 6.2 JTMO / e-Toll via EDC (`FR-7.1` – `FR-7.3`)

> ⚠️ **Asumsi yang harus dikonfirmasi:** Dokumen scope menyebut "JTMO" tanpa definisi. Kami
> mengasumsikan ini merujuk pada penerimaan **kartu uang elektronik berbasis e-Toll** (Mandiri
> e-Money, BCA Flazz, BRI Brizzi, BNI TapCash) melalui EDC/reader GPN, sebagaimana lazim digunakan
> untuk pembayaran parkir non-tunai di Indonesia. Jika "JTMO" adalah nama vendor/aggregator
> spesifik, spesifikasi adapter di bawah tetap berlaku, hanya implementasi konkretnya yang berubah.
> **Konfirmasi ini memblokir estimasi akhir modul ini** — lihat §18.

#### 6.2.1 Desain Adapter Vendor-Agnostik

Kita **tidak** menulis kode yang terikat satu merek EDC. Sebaliknya:

```go
type PaymentTerminal interface {
    Charge(ctx, amount int64, method CardType) (ChargeResult, error)
    Cancel(ctx, ref string) error
    LastBatch(ctx) (BatchSummary, error)
    Status(ctx) (TerminalStatus, error)
}

type ChargeResult struct {
    Approved      bool
    ApprovalCode  string   // kode persetujuan dari host
    CardType      CardType // FLAZZ | EMONEY | BRIZZI | TAPCASH | DEBIT
    MaskedPAN     string   // 6 digit awal + 4 akhir saja — sisanya JANGAN disimpan
    BalanceBefore int64    // saldo kartu sebelum (jika terminal melaporkannya)
    BalanceAfter  int64
    TerminalID    string
    BatchNo       string
    TraceNo       string
    RawResponse   []byte   // disimpan untuk forensik
}
```

Implementasi konkret: `edcTCPAdapter` (TCP :5001), `edcSerialAdapter` (RS232), `edcSimAdapter`.
Penambahan vendor baru = satu file baru, nol perubahan di logika bisnis.

#### 6.2.2 Alur Transaksi EDC

```
POS: kasir pilih "e-Toll / e-Money"
  │
  ├─► edge-api: buat payment record status=PENDING, simpan ke DB DULU
  │              (agar tidak hilang jika PC mati di tengah transaksi)
  │
  ├─► PaymentTerminal.Charge(amount, EMONEY)
  │     └─► TCP :5001 → EDC
  │            EDC menampilkan nominal, minta tap kartu
  │            Pengendara tap → EDC potong saldo lokal
  │            EDC balas approval + saldo akhir
  │
  ├─► timeout 60 detik
  │     └─► jika timeout: JANGAN anggap gagal. Panggil LastBatch()
  │           untuk cek apakah transaksi sebenarnya sukses.
  │           Ini mencegah double-charge.
  │
  ├─► sukses → payment.status=SETTLED, simpan approval_code + batch_no
  │            → vehicles_log.status=COMPLETED
  │            → audit_logs entry
  │            → outbox enqueue
  │            → WS event ke POS (< 500 ms, NFR-1)
  │            → gerbang OPENING
  │
  └─► gagal  → payment.status=FAILED, alasan disimpan
               → POS tampilkan pesan + tawarkan metode lain
```

#### 6.2.3 Aturan Keras EDC

1. **Tulis record `PENDING` ke database sebelum mengirim perintah ke EDC.** Jika PC mati di tengah,
   kita masih punya jejak untuk direkonsiliasi.
2. **Timeout ≠ gagal.** Selalu query `LastBatch()` sebelum menyimpulkan kegagalan. Double-charge
   pada pelanggan adalah kegagalan yang jauh lebih mahal daripada palang terlambat 5 detik.
3. **Jangan pernah menyimpan PAN lengkap.** Hanya masked PAN. Ini kepatuhan dasar, bukan opsional.
4. **Nominal dikirim dari sistem, bukan diketik kasir** (`FR-3.3` asli) — menghilangkan kesalahan
   ketik dan kecurangan nominal.
5. **`batch_no` + `approval_code` wajib tersimpan** — ini satu-satunya jembatan ke laporan settlement
   bank saat rekonsiliasi.

### 6.3 QRIS & E-Wallet (`FR-7.4`, `FR-7.5`)

Satu integrasi payment gateway (Midtrans **atau** Xendit) melayani QRIS dan seluruh e-wallet — inilah
alasan dokumen revisi menyebut *"satu paket vendor API"* dan menempatkannya bersama JTMO.

**Alur:**

1. Edge memanggil `cloud-api` → `cloud-api` memanggil PG (Edge **tidak pernah** memegang kredensial PG).
2. PG mengembalikan QR string + `order_id` + kedaluwarsa.
3. POS menampilkan QR. Pengendara memindai dan membayar.
4. PG mengirim **webhook** ke `cloud-api` (via Cloudflare, dengan verifikasi signature).
5. `cloud-api` mendorong event ke Edge melalui koneksi WS/gRPC persisten yang dibuka Edge.
6. POS ter-update **< 500 ms** dari settlement (`NFR-1`).
7. **Fallback:** jika event push tidak sampai dalam 5 detik, POS mulai long-polling status setiap
   2 detik hingga 5 menit.

**Aturan keras:**

- **Idempotensi webhook.** Simpan `provider_ref` dengan unique constraint. Webhook duplikat (PG
  sering mengirim ulang) tidak boleh membuat pembayaran ganda.
- **Verifikasi signature wajib.** Webhook tanpa signature valid ditolak `401` dan dicatat sebagai
  audit `critical`.
- **Kredensial PG hanya di Cloud.** Edge Node berada di lapangan dan secara fisik kurang aman.
- **Rekonsiliasi harian.** Cron membandingkan `payments` lokal vs laporan settlement PG; selisih
  memunculkan alert.

### 6.4 Rekonsiliasi Shift

Setiap kasir membuka dan menutup shift. Sistem menghitung:

| Kategori | Sumber angka |
|----------|--------------|
| Kas awal (float) | Input saat buka shift |
| Total tunai sistem | `SUM(payments WHERE method=CASH AND shift_id=?)` |
| Total EDC sistem | `SUM(payments WHERE method LIKE 'EDC%')` — dicocokkan manual dengan struk batch EDC |
| Total QRIS/e-wallet | `SUM(...)` — otomatis dari webhook |
| Kas fisik dilaporkan | Input kasir saat tutup shift |
| **Selisih** | `dilaporkan - (kas awal + tunai sistem)` |

Selisih ≠ 0 → shift ditandai `VARIANCE`, audit `warning`, wajib ada catatan alasan. Selisih di atas
ambang (konfigurasi per site) → audit `critical` + notifikasi SuperAdmin.

---

## 7. LPR / OCR & LOG OCR

### 7.1 Pipeline

```
Trigger (LD1/LD3 rising)
   │
   ├─► Ambil frame dari RTSP (buffer terakhir, bukan connect baru — hemat 200-400ms)
   │
   ├─► gRPC :50051 → lpr-svc, deadline 1000 ms
   │      ├─ YOLOv8n: deteksi kendaraan + crop area plat
   │      ├─ Klasifikasi jenis kendaraan → mobil|motor|truk|bus
   │      └─ EasyOCR/Tesseract: ekstraksi karakter + confidence
   │
   ├─► Normalisasi plat Indonesia:
   │      • uppercase, hapus spasi/strip
   │      • koreksi ambigu: O↔0, I↔1, S↔5, B↔8 (berdasar posisi)
   │      • validasi pola: ^[A-Z]{1,2}[0-9]{1,4}[A-Z]{0,3}$
   │
   ├─► Simpan gambar (lokal → Spaces async)
   │
   └─► Tulis ke ocr_logs SELALU — sukses maupun gagal
```

### 7.2 Log OCR (`FR-7.7`) — Item New Scope

Ini adalah item Rp2,5jt yang dokumen revisi sebut *"biaya rendah dampak tinggi untuk akurasi log"*.
Yang membuatnya berdampak tinggi: **log ini adalah satu-satunya cara mengukur dan memperbaiki
akurasi LPR di produksi.** Tanpa itu, klaim akurasi hanya tebakan.

Tabel `ocr_logs` menyimpan per pembacaan:

| Kolom | Guna |
|-------|------|
| `raw_text` | Output mentah OCR sebelum normalisasi |
| `normalized_plate` | Hasil setelah aturan normalisasi |
| `confidence` | Skor 0–1 dari engine |
| `verdict` | `AUTO_ACCEPT` (conf ≥ 0.85) / `NEEDS_REVIEW` (0.60–0.85) / `UNREAD` (< 0.60 atau timeout) |
| `corrected_plate` | Diisi jika operator mengoreksi |
| `corrected_by`, `corrected_at` | Jejak koreksi |
| `latency_ms` | Untuk memantau `NFR-1` |
| `engine_version` | Untuk membandingkan performa antar versi model |
| `image_key` | Referensi gambar untuk audit visual |

**Nilai turunan yang bisa dihitung dari tabel ini:**
- Akurasi aktual = `1 - (jumlah corrected / jumlah total)`
- Distribusi confidence → dasar untuk menyetel ambang
- p50/p95/p99 latency → verifikasi `NFR-1` dengan data nyata, bukan asumsi
- Kandidat data latih untuk perbaikan model di Fase 2

**Aturan:** setiap koreksi manual plat pada POS **otomatis** menulis `corrected_plate` — kasir tidak
perlu melakukan langkah tambahan. Log yang membebani operator adalah log yang tidak akan terisi.

### 7.3 LPR Bukan Gerbang Keputusan

Hasil LPR **tidak pernah** menjadi satu-satunya dasar membuka palang di Fase 1 (P2). Ia adalah
**data manifes** — pembanding dan bukti, bukan otorisasi. `FR-2.2 Smart Gate Lock` diimplementasikan
sebagai blacklist opsional yang **default-nya nonaktif**, dan ketika aktif, plat cocok blacklist
memicu peringatan ke operator, bukan penolakan otomatis.

Alasan: pada tahap awal, false-positive OCR yang menolak kendaraan sah lebih merusak daripada
false-negative yang meloloskan kendaraan yang mestinya ditandai.

---

## 8. MEMBERSHIP, ANTI-PASSBACK & AUTO-EXPIRATION

### 8.1 Registrasi RFID (`FR-5.1`)

- Kartu Mifare Classic/DESFire 13.56 MHz.
- Registrasi via dashboard: tap kartu di reader → UID terisi otomatis → isi data pemegang.
- Satu UID dapat terikat ke **beberapa plat** (keluarga) atau **satu plat** (ketat) — konfigurasi per site.
- Field: nama, unit/departemen, plat kendaraan, jenis kendaraan, tipe membership, `valid_from`,
  `valid_until`, site yang diizinkan (array — mendukung multi-tenant).

### 8.2 Anti-Passback (`FR-5.2`)

State per kartu: `OUT` (default) ↔ `IN`.

| Kejadian | State saat ini | Hasil |
|----------|----------------|-------|
| Tap di gerbang masuk | `OUT` | Diterima → state jadi `IN` |
| Tap di gerbang masuk | `IN` | **Ditolak** — audit `warning` `ANTIPASSBACK_VIOLATION` |
| Tap di gerbang keluar | `IN` | Diterima → state jadi `OUT` |
| Tap di gerbang keluar | `OUT` | Ditolak — audit `warning` |

**Reset harian.** Sistem nyata punya kasus tepi: petugas lupa tap keluar, listrik mati saat kartu
`IN`, dsb. Tanpa jalan keluar, kartu terkunci selamanya. Karena itu:

- Cron harian pukul 04:00 mereset kartu yang berstatus `IN` lebih dari **18 jam** menjadi `OUT`,
  dengan audit `warning` `ANTIPASSBACK_AUTO_RESET`.
- SuperAdmin dapat mereset manual per kartu dengan alasan wajib → audit `warning`.
- Ambang 18 jam dapat dikonfigurasi per site (lokasi 24 jam butuh nilai berbeda).

### 8.3 CRON Auto-Expiration (`FR-7.8`)

Scheduler Go (`robfig/cron`) di `edge-api`:

| Jadwal | Job | Aksi |
|--------|-----|------|
| `*/15 * * * *` | `membership_expiry` | Set `status=EXPIRED` untuk `valid_until < now()`; audit per kartu |
| `0 4 * * *` | `antipassback_reset` | Reset kartu `IN` > 18 jam (§8.2) |
| `0 * * * *` | `outbox_retry` | Coba ulang item outbox `FAILED` |
| `0 2 * * *` | `image_purge` | Hapus gambar lokal > 7 hari |
| `0 3 * * *` | `audit_chain_verify` | Verifikasi rantai hash penuh, alert jika rusak |
| `0 1 * * *` | `payment_reconcile` | Bandingkan `payments` vs laporan PG |

**Aturan:** setiap job wajib idempoten dan mengambil advisory lock PostgreSQL — mencegah eksekusi
ganda jika `edge-api` restart tepat saat job berjalan.

**Notifikasi kedaluwarsa:** H-7 dan H-1 sebelum `valid_until`, sistem membuat alert `info` di
dashboard. (Notifikasi email/WA masuk Fase 2 — tidak ada di anggaran.)

---

## 9. IMMUTABLE AUDIT LOG (`FR-5.3`)

### 9.1 Struktur Rantai Hash

```
current_hash = SHA256(
    previous_hash ‖ node_id ‖ seq ‖ event_type ‖ actor_id ‖
    canonical_json(payload) ‖ created_at_rfc3339nano
)
```

**Rantai per `(tenant_id, node_id)`, bukan global.** Ini keputusan penting: setiap Edge Node
menulis rantainya sendiri secara independen. Rantai global akan memaksa koordinasi antar node —
mustahil dipenuhi saat offline. Cloud memverifikasi setiap rantai secara terpisah.

- `seq` adalah `BIGINT` monoton per node, dari PostgreSQL sequence.
- Baris genesis per node: `previous_hash = '0'×64`, `seq = 0`.
- `canonical_json` = kunci terurut, tanpa whitespace — hash harus deterministik lintas bahasa.

### 9.2 Penegakan Immutability di Level Database

Trigger, bukan sekadar disiplin kode:

```sql
CREATE OR REPLACE FUNCTION audit_logs_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs bersifat append-only: operasi % ditolak', TG_OP;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_no_update BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();

REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM app_user;
```

> **Batas jujur yang harus disampaikan ke klien:** hash chaining mendeteksi **modifikasi**, bukan
> mencegahnya. Pemilik akses superuser database masih dapat menghapus seluruh tabel beserta
> triggernya. Yang membuat rantai bernilai adalah **verifikasi dari luar node**: Cloud menyimpan
> salinan `current_hash` yang telah tersinkronisasi, sehingga manipulasi di Edge terdeteksi saat
> perbandingan. Ini penting agar klien tidak salah paham bahwa sistem "tidak bisa diretas".

### 9.3 Peristiwa yang Wajib Dicatat

| Kategori | Peristiwa | Severity |
|----------|-----------|----------|
| Gerbang | Buka manual/override, tiket dibatalkan, palang ditembus (`UNAUTHORIZED_PASSAGE`) | `critical` |
| Transaksi | `VOID`, tiket hilang, penyesuaian tarif, foto tidak cocok | `warning`–`critical` |
| Pembayaran | Gagal, refund, selisih rekonsiliasi | `warning` |
| Membership | Registrasi, pemblokiran, pelanggaran anti-passback, reset manual | `normal`–`warning` |
| Konfigurasi | Perubahan tarif, kapasitas, konfigurasi hardware, peran pengguna | `warning` |
| Sistem | Login gagal >3×, controller offline, rantai hash rusak, kertas habis | `warning`–`critical` |
| Data | Bulk import CSV (dengan jumlah baris + checksum file) | `warning` |

### 9.4 Verifikasi Rantai

- **Terjadwal:** cron 03:00 memverifikasi rantai penuh per node.
- **On-demand:** tombol "Verify Chain" di halaman Audit Ledger (§12.12).
- **Saat sync:** `cloud-api` memverifikasi kontinuitas setiap batch yang masuk; batch dengan
  `previous_hash` yang tidak menyambung ditolak dan memicu alert `critical`.
- **Saat rantai rusak:** sistem **tidak** berhenti beroperasi (P1) — ia menaikkan alert `critical`,
  menandai rentang `seq` yang terdampak, dan tetap menulis entri baru dari titik terakhir yang valid.

---

## 10. SINKRONISASI EDGE → CLOUD (`FR-1.2`)

### 10.1 Pola Transactional Outbox

Bukan message queue terpisah — outbox adalah tabel di PostgreSQL lokal yang **ditulis dalam
transaksi yang sama** dengan data bisnisnya. Ini menjamin: tidak ada transaksi tercatat yang gagal
masuk antrean sync, dan tidak ada item antrean tanpa transaksi.

```sql
BEGIN;
  INSERT INTO vehicles_log (...) VALUES (...);
  INSERT INTO payments (...) VALUES (...);
  INSERT INTO audit_logs (...) VALUES (...);
  INSERT INTO sync_outbox (aggregate, aggregate_id, payload, status)
       VALUES ('vehicles_log', $1, $2, 'PENDING');
COMMIT;
```

### 10.2 Sync Agent

- Goroutine di `edge-api`, tick tiap **10 detik**.
- Ambil hingga **200 item** `PENDING` terurut `created_at`, kirim sebagai satu batch gRPC.
- Backoff eksponensial saat gagal: 10s → 30s → 2m → 10m → 30m (maksimum).
- Item gagal **5×** → status `FAILED`, alert `warning`, tetap disimpan (tidak pernah dibuang).
- **Urutan dijaga per agregat.** `audit_logs` wajib berurutan `seq` — dikirim dalam batch terpisah
  dari data transaksi agar kegagalan satu tidak merusak urutan yang lain.
- Gambar disinkronkan pada jalur terpisah dengan prioritas lebih rendah — 1 foto tidak boleh
  memblokir 200 transaksi.

### 10.3 Idempotensi di Sisi Cloud

`cloud-api` menerima batch dan melakukan `INSERT ... ON CONFLICT (id) DO NOTHING`. Karena PK adalah
UUIDv7 yang dibuat di Edge, pengiriman ulang aman. Untuk `audit_logs`, konflik pada
`(node_id, seq)` yang **isinya berbeda** adalah tanda manipulasi → alert `critical`.

### 10.4 Perilaku Mode Offline

| Aspek | Perilaku |
|-------|----------|
| Deteksi | Health check ke `cloud-api` tiap 30 detik; 3 kegagalan → mode `OFFLINE` |
| Indikator | Badge "OFFLINE" di POS & dashboard lokal; lampu gerbang kuning blink |
| Antrean | Tumbuh di `sync_outbox`; alert `warning` saat > 5.000 item |
| Metode bayar | QRIS & e-wallet dinonaktifkan (§6.1) |
| Pemulihan | Saat kembali online, drain otomatis dengan rate limit agar tidak membanjiri Cloud |
| Kapasitas | Dengan 2.000 transaksi/hari, disk 100GB menampung > 1 tahun antrean |

---

## 11. MODEL DATA (POSTGRESQL)

Skema ini menggantikan mock Zustand store (`FR-7.6`). Nama tabel & field diselaraskan dengan tipe
yang sudah ada di prototype (`ParkingLot`, `AuditEvent`) agar migrasi frontend minimal.

### 11.1 Diagram Relasi

```
tenants ─┬─► sites ─┬─► tariffs
         │          ├─► slots_map
         │          ├─► gates ─► devices
         │          ├─► memberships ─┐
         │          ├─► shifts ──────┼─┐
         │          └─► alerts       │ │
         ├─► users ────────────────────┤
         │                           │ │
         └─► vehicles_log ◄──────────┘ │
                  ├─► payments ◄────────┘
                  └─► ocr_logs

         audit_logs   (append-only, rantai per node)
         sync_outbox  (antrean replikasi)
```

### 11.2 DDL Inti

```sql
-- ══════════ TENANCY & LOKASI ══════════
CREATE TABLE tenants (
    id           UUID PRIMARY KEY,
    code         TEXT UNIQUE NOT NULL,
    name         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sites (                      -- "Lahan Parkir"
    id           UUID PRIMARY KEY,
    tenant_id    UUID NOT NULL REFERENCES tenants(id),
    code         TEXT NOT NULL,           -- mis. "mall_jabar"
    name         TEXT NOT NULL,
    city         TEXT,
    address      TEXT,
    timezone     TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    grace_minutes        INT NOT NULL DEFAULT 15,
    peak_multiplier      NUMERIC(4,2) NOT NULL DEFAULT 1.00,
    peak_windows         JSONB NOT NULL DEFAULT '[]',
    max_daily_rate       BIGINT,
    lost_ticket_penalty  BIGINT NOT NULL DEFAULT 0,
    antipassback_reset_hours INT NOT NULL DEFAULT 18,
    cash_variance_threshold  BIGINT NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, code)
);

CREATE TABLE tariffs (
    id           UUID PRIMARY KEY,
    site_id      UUID NOT NULL REFERENCES sites(id),
    vehicle_type TEXT NOT NULL,           -- mobil|motor|truk|bus
    base_rate    BIGINT NOT NULL,         -- rupiah utuh, per jam
    first_hour_rate BIGINT,               -- NULL = sama dengan base_rate
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_to    TIMESTAMPTZ,          -- NULL = masih berlaku
    UNIQUE (site_id, vehicle_type, effective_from)
);
-- Tarif bersifat versioned: perubahan = baris baru, bukan UPDATE.
-- Transaksi lama tetap dapat direkalkulasi dengan tarif saat itu.

CREATE TABLE slots_map (
    id            UUID PRIMARY KEY,
    site_id       UUID NOT NULL REFERENCES sites(id),
    vehicle_type  TEXT NOT NULL,
    capacity      INT  NOT NULL,
    zone          TEXT,
    UNIQUE (site_id, vehicle_type, zone)
);

-- ══════════ PERANGKAT ══════════
CREATE TABLE gates (
    id            UUID PRIMARY KEY,
    site_id       UUID NOT NULL REFERENCES sites(id),
    code          TEXT NOT NULL,          -- "GATE-IN-01"
    kind          TEXT NOT NULL,          -- ENTRY|EXIT
    controller_addr SMALLINT NOT NULL,    -- 0x01 / 0x02
    transport     TEXT NOT NULL,          -- serial|tcp|sim
    endpoint      TEXT NOT NULL,          -- "COM3" | "10.0.0.5:5001"
    config        JSONB NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'active',
    UNIQUE (site_id, code)
);

CREATE TABLE devices (
    id            UUID PRIMARY KEY,
    gate_id       UUID REFERENCES gates(id),
    site_id       UUID NOT NULL REFERENCES sites(id),
    kind          TEXT NOT NULL,   -- BARRIER|LOOP|PRINTER|RFID|CAMERA|EDC|LIGHT
    label         TEXT NOT NULL,   -- "LD1", "Printer Masuk"
    address       TEXT,
    config        JSONB NOT NULL DEFAULT '{}',
    last_seen_at  TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'unknown'  -- online|offline|fault|unknown
);

-- ══════════ PENGGUNA & MEMBER ══════════
CREATE TABLE users (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    email         TEXT NOT NULL,
    password_hash TEXT NOT NULL,          -- argon2id
    full_name     TEXT NOT NULL,
    role          TEXT NOT NULL,          -- SuperAdmin|Auditor|Kasir
    site_scope    UUID[] NOT NULL DEFAULT '{}',  -- kosong = semua site tenant
    status        TEXT NOT NULL DEFAULT 'active',
    last_login_at TIMESTAMPTZ,
    failed_logins INT NOT NULL DEFAULT 0,
    UNIQUE (tenant_id, email)
);

CREATE TABLE memberships (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    rfid_uid      TEXT NOT NULL,
    holder_name   TEXT NOT NULL,
    unit_label    TEXT,
    plates        TEXT[] NOT NULL DEFAULT '{}',
    vehicle_type  TEXT NOT NULL,
    site_scope    UUID[] NOT NULL DEFAULT '{}',
    valid_from    DATE NOT NULL,
    valid_until   DATE NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active',   -- active|expired|blocked
    presence      TEXT NOT NULL DEFAULT 'OUT',      -- IN|OUT  (anti-passback)
    presence_since TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, rfid_uid)
);
CREATE INDEX idx_memberships_expiry ON memberships (valid_until)
    WHERE status = 'active';

-- ══════════ TRANSAKSI ══════════
CREATE TABLE vehicles_log (
    id            UUID PRIMARY KEY,                  -- UUIDv7 (dibuat di Edge)
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    site_id       UUID NOT NULL REFERENCES sites(id),
    ticket_code   TEXT,
    vehicle_type  TEXT NOT NULL,
    membership_id UUID REFERENCES memberships(id),
    plate_in      TEXT,
    plate_out     TEXT,
    img_in_key    TEXT,
    img_out_key   TEXT,
    entry_time    TIMESTAMPTZ NOT NULL,
    exit_time     TIMESTAMPTZ,
    entry_gate_id UUID REFERENCES gates(id),
    exit_gate_id  UUID REFERENCES gates(id),
    duration_min  INT,
    amount        BIGINT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL,     -- IN_PREMISES|COMPLETED|VOID|DISPUTE
    flags         TEXT[] NOT NULL DEFAULT '{}',
                  -- needs_review | no_show | lost_ticket | unregistered_entry
                  -- | photo_mismatch | manual_override
    operator_id   UUID REFERENCES users(id),
    shift_id      UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_vl_ticket ON vehicles_log (site_id, ticket_code)
    WHERE ticket_code IS NOT NULL;
CREATE INDEX idx_vl_active  ON vehicles_log (site_id, status)
    WHERE status = 'IN_PREMISES';
CREATE INDEX idx_vl_entry   ON vehicles_log (site_id, entry_time DESC);
CREATE INDEX idx_vl_plate   ON vehicles_log (site_id, plate_in);

CREATE TABLE payments (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    site_id        UUID NOT NULL,
    vehicles_log_id UUID NOT NULL REFERENCES vehicles_log(id),
    shift_id       UUID,
    method         TEXT NOT NULL,   -- CASH|EDC_EMONEY|EDC_DEBIT|QRIS|EWALLET|MEMBER
    amount         BIGINT NOT NULL,
    tendered       BIGINT,          -- untuk CASH
    change_given   BIGINT,
    status         TEXT NOT NULL,   -- PENDING|SETTLED|FAILED|REFUNDED
    provider       TEXT,            -- midtrans|xendit|edc_vendor
    provider_ref   TEXT,            -- order_id / trace no
    card_type      TEXT,            -- FLAZZ|EMONEY|BRIZZI|TAPCASH|DEBIT
    masked_pan     TEXT,            -- HANYA masked
    approval_code  TEXT,
    terminal_id    TEXT,
    batch_no       TEXT,
    balance_after  BIGINT,
    raw_response   JSONB,
    settled_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_pay_provider_ref ON payments (provider, provider_ref)
    WHERE provider_ref IS NOT NULL;   -- idempotensi webhook

CREATE TABLE ocr_logs (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    site_id        UUID NOT NULL,
    vehicles_log_id UUID REFERENCES vehicles_log(id),
    gate_id        UUID REFERENCES gates(id),
    captured_at    TIMESTAMPTZ NOT NULL,
    raw_text       TEXT,
    normalized_plate TEXT,
    confidence     NUMERIC(4,3),
    verdict        TEXT NOT NULL,   -- AUTO_ACCEPT|NEEDS_REVIEW|UNREAD
    corrected_plate TEXT,
    corrected_by   UUID REFERENCES users(id),
    corrected_at   TIMESTAMPTZ,
    latency_ms     INT,
    engine_version TEXT,
    image_key      TEXT
);
CREATE INDEX idx_ocr_review ON ocr_logs (site_id, captured_at DESC)
    WHERE verdict <> 'AUTO_ACCEPT';

CREATE TABLE shifts (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    site_id        UUID NOT NULL,
    operator_id    UUID NOT NULL REFERENCES users(id),
    opened_at      TIMESTAMPTZ NOT NULL,
    closed_at      TIMESTAMPTZ,
    opening_float  BIGINT NOT NULL DEFAULT 0,
    declared_cash  BIGINT,
    system_cash    BIGINT,
    system_edc     BIGINT,
    system_qris    BIGINT,
    variance       BIGINT,
    note           TEXT,
    status         TEXT NOT NULL DEFAULT 'OPEN'   -- OPEN|CLOSED|VARIANCE
);

-- ══════════ AUDIT & SYNC ══════════
CREATE TABLE audit_logs (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    site_id        UUID,
    node_id        TEXT NOT NULL,
    seq            BIGINT NOT NULL,
    event_type     TEXT NOT NULL,
    severity       TEXT NOT NULL,   -- normal|warning|critical
    actor_id       UUID,
    actor_label    TEXT NOT NULL,
    actor_role     TEXT NOT NULL,   -- SuperAdmin|Auditor|Kasir|System
    gate_label     TEXT,
    device_label   TEXT,
    summary        TEXT NOT NULL,
    payload        JSONB NOT NULL DEFAULT '{}',
    previous_hash  CHAR(64) NOT NULL,
    current_hash   CHAR(64) NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    UNIQUE (node_id, seq)
);
CREATE INDEX idx_audit_sev ON audit_logs (tenant_id, severity, created_at DESC);

CREATE TABLE sync_outbox (
    id             BIGSERIAL PRIMARY KEY,
    aggregate      TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    payload        JSONB NOT NULL,
    status         TEXT NOT NULL DEFAULT 'PENDING',  -- PENDING|SENT|FAILED
    attempts       INT  NOT NULL DEFAULT 0,
    last_error     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at        TIMESTAMPTZ
);
CREATE INDEX idx_outbox_pending ON sync_outbox (created_at)
    WHERE status = 'PENDING';

CREATE TABLE alerts (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    site_id        UUID,
    type           TEXT NOT NULL,
    severity       TEXT NOT NULL,
    message        TEXT NOT NULL,
    device_id      UUID REFERENCES devices(id),
    context        JSONB NOT NULL DEFAULT '{}',
    opened_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_by UUID REFERENCES users(id),
    acknowledged_at TIMESTAMPTZ,
    resolved_at    TIMESTAMPTZ
);
```

### 11.3 Strategi Migrasi dari Mock Store (`FR-7.6`)

Prototype menyimpan seluruh state di `auditStore.ts` (41KB Zustand). Migrasi dilakukan bertahap
agar demo tidak pernah rusak di tengah jalan:

| Langkah | Aksi | Hasil |
|---------|------|-------|
| 1 | Ekstrak seluruh tipe dari store → paket `types/` bersama | Kontrak data eksplisit |
| 2 | Bangun `api-client` dengan **antarmuka identik** dengan aksi store saat ini | Komponen tidak berubah |
| 3 | Ganti isi setiap aksi store: dari mutasi lokal → panggilan API | Satu halaman per commit |
| 4 | Store menyimpan cache + status UI saja; sumber kebenaran = server | State bersih |
| 5 | Feature flag `VITE_USE_MOCK` | Demo offline tetap mungkin |
| 6 | Seed script dari dummy data yang ada → PostgreSQL | Demo klien tetap identik isinya |

**Aturan:** flag `VITE_USE_MOCK` dipertahankan sampai akhir proyek. Kemampuan mendemokan sistem
tanpa backend hidup terlalu berharga untuk dibuang — terutama untuk presentasi ke klien.

---

## 12. SPESIFIKASI DASHBOARD

Dasar: 12 halaman pada `Sidebar.tsx` prototype, dikelompokkan dalam 4 kategori. Setiap halaman di
bawah menyatakan sumber data nyata, aksi yang tersedia, dan hak akses.

### 12.0 Matriks RBAC

| Halaman | SuperAdmin | Auditor | Kasir |
|---------|:----------:|:-------:|:-----:|
| Dashboard Overview | ✅ | ✅ (read) | ❌ |
| Catatan Keuangan | ✅ | ✅ (read) | ❌ |
| Volume & Jenis Kendaraan | ✅ | ✅ (read) | ❌ |
| Mapping Slot Parkir | ✅ | ❌ | ❌ |
| Konfigurasi Lahan | ✅ | ❌ | ❌ |
| Rekonsiliasi Shift | ✅ | ✅ (read) | ✅ (shift sendiri) |
| Notifikasi & Alerts | ✅ | ✅ (read) | ❌ |
| Field Monitor | ✅ | ❌ | ❌ |
| Exit POS Cashier | ✅ | ❌ | ✅ |
| RFID Memberships | ✅ | ✅ (read) | ❌ |
| Hardware Config (COM) | ✅ | ❌ | ❌ |
| Audit Ledger Chain | ✅ | ✅ (read + verify) | ❌ |

RBAC di-*enforce* di **dua lapis**: menu frontend (UX) dan middleware backend (keamanan). Frontend
saja tidak pernah cukup.

### 12.1 Dashboard Overview

**Kartu metrik (real-time via WS):** kendaraan di dalam · slot tersedia per jenis · pendapatan hari
ini · transaksi hari ini · durasi rata-rata · tingkat okupansi %.

**Panel status:** kesehatan tiap gerbang (online/offline/fault) · status sinkronisasi (synced /
N pending / offline) · status rantai audit (verified/broken) · alert aktif.

**Grafik (`FR-6.2`):** okupansi 24 jam terakhir (line) · pendapatan 7 hari (bar) · komposisi metode
bayar (donut).

**Filter multi-tenant (`FR-6.4`):** pemilih site di TopBar. SuperAdmin melihat "Semua Site" agregat
atau satu site. Pemilihan tersimpan di preferensi pengguna.

### 12.2 Catatan Keuangan

Tabel transaksi berbayar dengan filter (rentang tanggal, metode, kasir, site, status) dan kolom:
waktu · tiket · plat · jenis · durasi · tarif · metode · kasir · status settlement.

**Ringkasan:** total per metode, MDR estimasi (0,7% untuk QRIS/e-wallet sesuai OpEx), net.
**Ekspor CSV & XLSX** (`FR-6.3`). **Drill-down** ke detail transaksi termasuk foto masuk/keluar.

### 12.3 Volume & Jenis Kendaraan

Heatmap jam × hari · distribusi jenis kendaraan · durasi parkir (histogram) · perbandingan antar
site · jam sibuk untuk kalibrasi `peak_windows`. Ekspor CSV.

### 12.4 Mapping Slot Parkir

Kapasitas vs terisi per jenis & zona, visual. Sumber angka terisi:
`COUNT(vehicles_log WHERE status='IN_PREMISES')` per jenis. Aksi: ubah kapasitas (audit `warning`),
tambah/hapus zona. Peringatan saat okupansi > 90%.

### 12.5 Konfigurasi Lahan

Sudah ada di prototype (`LocationConfigPage.tsx`). Yang ditambahkan untuk produksi:

- Field baru: timezone, grace period, tarif maksimum harian, denda tiket hilang, ambang selisih kas,
  jam reset anti-passback.
- **Tarif menjadi versioned** — mengubah tarif membuat baris `tariffs` baru dengan `effective_from`,
  tidak menimpa yang lama.
- Setiap perubahan → audit `warning` dengan nilai lama & baru.
- Penambahan site otomatis membuat `slots_map`, `tariffs`, dan `gates` default (sesuai catatan
  "Database Integrity Policy" pada prototype).

### 12.6 Rekonsiliasi Shift

Buka shift (input kas awal) → panel berjalan (transaksi shift, total per metode) → tutup shift
(input kas fisik, sistem hitung selisih). Riwayat shift dengan penanda `VARIANCE`. Cetak laporan
shift (PDF). Kasir hanya melihat shift-nya sendiri.

### 12.7 Notifikasi & Alerts

Daftar alert aktif diurutkan severity, dengan aksi acknowledge (wajib catatan untuk `critical`) dan
resolve. Filter per site/jenis/severity. Riwayat alert. Real-time via WS.

### 12.8 Field Monitor (sebelumnya "Field Simulator")

Di produksi, halaman ini berubah fungsi dari simulator menjadi **monitor gerbang langsung**:

- State machine gerbang secara real-time (state saat ini, transisi terakhir).
- Status loop detector langsung (LD1–LD4).
- Snapshot LPR terakhir + hasil OCR + confidence.
- Tombol **override manual**: buka palang (wajib alasan → audit `critical`).
- Toggle **mode simulator** (khusus SuperAdmin) untuk demo & pelatihan tanpa perangkat fisik.

### 12.9 Exit POS Cashier

Antarmuka kasir, dioptimalkan untuk kecepatan (target < 15 detik per kendaraan):

- Panel kendaraan terdeteksi (dari LD3 + LPR).
- **Komparasi foto berdampingan** (`FR-3.1`): foto masuk vs snapshot langsung, dengan tombol
  "Cocok" / "Tidak Cocok" yang besar dan jelas.
- Rincian tarif (waktu masuk, durasi, jenis, tarif, total).
- Pemilih metode bayar — **yang offline-incapable otomatis disabled saat mode offline** (§6.1).
- Aksi khusus: tiket hilang, input tiket manual, batalkan transaksi (audit).
- Shortcut keyboard untuk seluruh aksi utama — kasir tidak boleh bergantung pada mouse.
- **Update UI < 500 ms setelah settlement** (`NFR-1`) via WS.

### 12.10 RFID Memberships

CRUD member + registrasi via tap kartu. Kolom: UID · nama · unit · plat · jenis · masa berlaku ·
status · presence (IN/OUT). Aksi: blokir/aktifkan, perpanjang, reset anti-passback (audit).
**Bulk import/export CSV** (`FR-6.3`) — lihat §12.13.

### 12.11 Hardware Config (COM)

Konfigurasi & diagnostik perangkat, mengembangkan `HardwareBridgeSim.tsx`:

- Pemetaan port per gerbang (COM/TCP, baud, alamat controller).
- Status koneksi langsung + waktu heartbeat terakhir.
- **Telemetry frame TX/RX** dalam hex, live streaming (fitur yang sudah ada di prototype dan sangat
  berharga saat troubleshooting lapangan).
- Uji perangkat: buka/tutup palang, tes cetak, tes lampu, baca loop — semuanya ter-audit.
- Status printer (kertas, jam) dan status terminal EDC.

### 12.12 Audit Ledger Chain

Timeline peristiwa dengan `previous_hash`/`current_hash`, status verifikasi, dan status sync per
entri (mengikuti tipe `AuditEvent` yang sudah ada di prototype). Aksi: **Verify Chain** (verifikasi
penuh on-demand), filter per severity/aktor/jenis, ekspor CSV untuk audit eksternal. Banner merah
mencolok jika rantai rusak, menunjukkan rentang `seq` yang terdampak.

### 12.13 Bulk CSV Import/Export (`FR-6.3`)

| Entitas | Import | Export |
|---------|:------:|:------:|
| Memberships | ✅ | ✅ |
| Tarif | ✅ | ✅ |
| Sites | ✅ | ✅ |
| Users | ✅ | ✅ |
| Transaksi | ❌ | ✅ |
| Payments | ❌ | ✅ |
| Audit logs | ❌ | ✅ |

**Alur import (wajib 3 langkah):** unggah → **pratinjau + validasi** (tampilkan baris valid/invalid
dengan alasan per baris) → konfirmasi. Import bersifat transaksional: satu baris gagal → seluruh
batch dibatalkan. Setiap import menulis audit `warning` berisi nama file, jumlah baris, dan
checksum SHA-256 file.

> Import tanpa langkah pratinjau adalah cara tercepat merusak data produksi. Langkah ini tidak
> boleh dihilangkan demi menghemat waktu development.

### 12.14 Multi-Tenant di Seluruh Flow (`FR-6.4`)

- Pemilih site di TopBar, berlaku global untuk seluruh halaman.
- Setiap query backend **wajib** menyertakan filter `tenant_id` — di-enforce di lapisan repository,
  bukan diserahkan ke pemanggil.
- `site_scope` pada `users` membatasi site yang dapat diakses.
- Mode agregat "Semua Site" hanya untuk SuperAdmin.
- **Uji keamanan wajib:** pengguna tenant A tidak boleh dapat mengakses data tenant B melalui
  manipulasi ID di URL/payload. Ini masuk daftar tes QA (§16).

---

## 13. API SURFACE

### 13.1 Edge API (lokal, dikonsumsi POS & Field Monitor)

```
POST   /api/v1/entry/ticket              # terbitkan tiket (dipicu tombol)
POST   /api/v1/entry/member              # validasi tap RFID masuk
GET    /api/v1/exit/lookup?ticket=|uid=|plate=
POST   /api/v1/exit/calculate            # hitung tarif
POST   /api/v1/exit/payment              # proses pembayaran
POST   /api/v1/exit/complete             # selesaikan & buka palang
POST   /api/v1/gate/{id}/open            # override manual (audit critical)
POST   /api/v1/gate/{id}/close
GET    /api/v1/gate/{id}/state
POST   /api/v1/device/{id}/test
GET    /api/v1/health                    # status device, sync, chain
WS     /api/v1/stream                    # event real-time
```

**Event WebSocket:** `gate.state_changed` · `loop.changed` · `lpr.result` · `payment.settled` ·
`printer.status` · `device.status` · `alert.raised` · `sync.status` · `vehicle.entered` ·
`vehicle.exited`.

### 13.2 Cloud API (dashboard & sync)

```
POST   /api/v1/auth/login | refresh | logout
GET    /api/v1/sites | /api/v1/sites/{id}
POST   /api/v1/sites                         # SuperAdmin
GET    /api/v1/tariffs?site_id=
POST   /api/v1/tariffs                       # buat versi tarif baru
GET    /api/v1/transactions?site_id=&from=&to=&status=&method=
GET    /api/v1/transactions/{id}
GET    /api/v1/reports/financial | /traffic | /occupancy
GET    /api/v1/memberships | POST | PATCH
POST   /api/v1/memberships/import            # bulk CSV
GET    /api/v1/memberships/export
GET    /api/v1/shifts | POST /open | POST /{id}/close
GET    /api/v1/alerts | POST /{id}/acknowledge
GET    /api/v1/audit?severity=&from=&to=
POST   /api/v1/audit/verify                  # verifikasi rantai
GET    /api/v1/ocr-logs?verdict=             # analitik akurasi LPR

# Internal (mTLS, khusus Edge)
POST   /internal/v1/sync/batch
POST   /internal/v1/payment/qris             # mint QR (kredensial PG hanya di Cloud)
POST   /webhook/payment/{provider}           # webhook PG, verifikasi signature
```

**Konvensi:** versi di path · pagination cursor-based · error RFC 7807 (`application/problem+json`)
· seluruh payload divalidasi struct di Go (`NFR-3`) · rate limit per pengguna & per IP.

---

## 14. KEAMANAN & RBAC

| Kontrol | Implementasi |
|---------|--------------|
| Autentikasi | JWT access token 15 menit + refresh token 7 hari (rotasi, disimpan httpOnly) |
| Password | Argon2id; kunci akun 15 menit setelah 5 kegagalan; audit `warning` |
| Otorisasi | Middleware role + `site_scope`, dicek di **setiap** endpoint |
| Edge ↔ Cloud | mTLS dengan sertifikat klien per node (`FR-1.4`); fallback JWT-over-HTTPS |
| Kredensial | Seluruhnya via `.env` / secret manager — **nol hardcoded** (`NFR-3`) |
| Validasi input | Struct validation Go di setiap endpoint |
| Data kartu | Hanya masked PAN; PAN lengkap tidak pernah disimpan atau di-log |
| Transport | TLS 1.3 via Cloudflare; HSTS |
| Isolasi tenant | Filter `tenant_id` di lapisan repository, bukan di controller |
| Audit | Seluruh aksi istimewa tercatat dengan aktor, waktu, dan alasan |
| Logging | Structured (`slog`), **tanpa PII/PAN**, level per komponen |
| Rahasia di Edge | Edge tidak memegang kredensial PG maupun sertifikat root |

---

## 15. NON-FUNCTIONAL REQUIREMENTS

| ID | Requirement | Target | Cara verifikasi |
|----|-------------|--------|-----------------|
| `NFR-1.1` | Ekstraksi LPR (snapshot → JSON) | ≤ 1.000 ms | p95 dari `ocr_logs.latency_ms` |
| `NFR-1.2` | Cetak tiket sejak tombol ditekan | ≤ 1.000 ms | Metrik `gate_state_transitions` |
| `NFR-1.3` | Update UI POS setelah settlement | < 500 ms | Timestamp WS event vs render |
| `NFR-1.4` | Palang mulai membuka setelah otorisasi | < 300 ms | Telemetry serial |
| `NFR-2.1` | Ketersediaan fungsi inti Edge | 99,99% | Uptime monitor + chaos test |
| `NFR-2.2` | Nol kehilangan transaksi saat offline | 100% | Tes cabut jaringan 24 jam |
| `NFR-2.3` | Pemulihan setelah restart `edge-api` | < 15 detik | Tes restart |
| `NFR-3` | Keamanan | Lihat §14 | Checklist + tes isolasi tenant |
| `NFR-4.1` | Dashboard memuat 10.000 transaksi | < 2 detik | Load test |
| `NFR-4.2` | Kapasitas Cloud | 20 site / 50 pengguna konkuren | Load test |
| `NFR-5` | Semua layanan dalam Docker, restart `always` | — | Review compose |

**Catatan jujur tentang 99,99%:** angka ini berarti maksimum ~52 menit downtime per tahun. Pada satu
PC Edge tanpa redundansi perangkat keras, angka ini **tidak dapat dijamin oleh software saja** —
kegagalan disk, PSU, atau listrik akan melampauinya. Yang dapat kita jamin: perangkat lunak tidak
menjadi penyebab downtime, dan tidak ada transaksi hilang saat terjadi gangguan. Untuk benar-benar
mencapai 99,99% dibutuhkan UPS, disk redundan, dan Edge Node cadangan — itu adalah pengadaan
perangkat keras, di luar scope software.

---

## 16. TIMELINE 4 MINGGU

**Asumsi:** 2 developer fullstack · 5 hari kerja/minggu · **40 dev-day tersedia**.

### 16.1 Uji Kelayakan Terlebih Dahulu

Estimasi jujur atas scope penuh:

| Area kerja | Estimasi (dev-day) |
|------------|-------------------:|
| Skema DB + migrasi + repository layer | 4 |
| Backend Edge: scaffold, config, DI, health | 3 |
| Hardware bridge: codec, port manager, simulator | 5 |
| State machine masuk + keluar | 5 |
| Fare engine + aturan kasus tepi | 2 |
| LPR gRPC + normalisasi + `ocr_logs` | 4 |
| Pembayaran: adapter EDC + QRIS/e-wallet + webhook | 7 |
| Membership + anti-passback + CRON | 3 |
| Audit chain + verifikasi | 2 |
| Sync agent + Cloud receiver | 4 |
| Cloud API + multi-tenant + agregasi | 4 |
| Migrasi dashboard mock → API (12 halaman) | 8 |
| POS keluar (produksi) | 3 |
| Bulk CSV + ekspor | 2 |
| Auth/RBAC produksi | 2 |
| Docker, deploy, Cloudflare Tunnel | 2 |
| QA, integrasi, perbaikan bug | 5 |
| **TOTAL** | **65** |

**65 dev-day dibutuhkan vs 40 tersedia — kelebihan 62%.** Scope penuh tidak muat dalam 4 minggu
dengan 2 orang. Ini bukan masalah kecepatan kerja; ini aritmatika. Rencana di bawah menyelesaikannya
dengan memindahkan 25 dev-day ke Fase 1b/2 (§17), sehingga tersisa **40 dev-day** — pas, tanpa
buffer.

### 16.2 Kalender Kerja — Catatan Hari Libur

Development dimulai **Senin 27 Juli 2026** (dokumen ini selesai 22 Juli; sisa minggu ini untuk
menjawab Q1/Q7/Q10 dan menyiapkan lingkungan).

| Minggu | Tanggal | Hari kerja | Catatan |
|--------|---------|:----------:|---------|
| 1 | 27–31 Juli | 5 | — |
| 2 | 3–7 Agustus | 5 | — |
| 3 | 10–14 Agustus | 5 | — |
| 4 | 17–21 Agustus | **4** | **17 Agustus = Hari Kemerdekaan RI (Senin), libur nasional** |
| **Total** | | **19 hari** | |

**Dampak:** 19 hari × 2 developer = **38 dev-day tersedia**, bukan 40. Setelah pemangkasan §17
menyisakan kebutuhan 40 dev-day, terdapat **defisit 2 dev-day**.

**Tiga opsi penyelesaian — perlu keputusan sebelum mulai:**

| Opsi | Konsekuensi |
|------|-------------|
| **A. Geser rilis ke Senin 24 Agustus** *(rekomendasi)* | +2 hari kerja, tepat menutup defisit. Masih dalam rentang "maksimum sebulan" dari tanggal mulai |
| B. Turunkan 1 item lagi ke Fase 2 | Kandidat paling aman: bulk CSV import (2 hari) — ekspor tetap ada, import manual sementara |
| C. Tetap 21 Agustus, terima defisit | Berarti memakan waktu QA. **Tidak direkomendasikan** — QA sudah dipangkas dari 2 fase ke 1 |

### 16.3 Rencana Per Minggu

**Pembagian peran:** **Dev A** = Edge, hardware, pembayaran, sync. **Dev B** = Cloud, dashboard,
POS, auth.

#### Minggu 1 (27–31 Juli) — Fondasi

| Dev | Pekerjaan | Output |
|-----|-----------|--------|
| A | Skema PostgreSQL + migrasi (goose) + repository layer · scaffold `edge-api` (Fiber, config, DI, structured logging, health) · **codec protokol serial + simulator perangkat lengkap** | DB siap · simulator jalan |
| B | Auth produksi (JWT, Argon2id, RBAC middleware) · paket `types/` bersama + `api-client` · CRUD sites/tariffs/slots (Konfigurasi Lahan jalan di atas API nyata) · Docker Compose | Login nyata · 1 halaman lepas dari mock |

**Gerbang keluar minggu 1:** migrasi jalan · simulator merespons seluruh perintah §5.3 · login
dengan RBAC berfungsi · Konfigurasi Lahan membaca/menulis PostgreSQL.

#### Minggu 2 (3–7 Agustus) — Alur Inti Gerbang

| Dev | Pekerjaan | Output |
|-----|-----------|--------|
| A | **State machine masuk** lengkap dengan timeout & degradasi · **state machine keluar** · anti-tailgating + interlock · WS event bus · LPR gRPC client + `ocr_logs` | Siklus masuk-keluar penuh di atas simulator |
| B | Exit POS produksi (komparasi foto, rincian tarif, shortcut) · fare engine + kasus tepi · Dashboard Overview + Volume Kendaraan + Mapping Slot di atas data nyata | POS dapat menyelesaikan transaksi tunai |

**Gerbang keluar minggu 2:** kendaraan dapat masuk (tiket tercetak, palang buka, tercatat) dan
keluar (teridentifikasi, tarif benar, bayar tunai, palang buka) sepenuhnya di atas simulator.

#### Minggu 3 (10–14 Agustus) — Pembayaran, Member, Audit

| Dev | Pekerjaan | Output |
|-----|-----------|--------|
| A | Interface `PaymentTerminal` + **adapter EDC simulator** + adapter TCP · integrasi QRIS/e-wallet via Cloud + webhook + idempotensi · rantai hash audit + verifikasi | Pembayaran non-tunai jalan |
| B | Membership + registrasi RFID + anti-passback · CRON (6 job) · Catatan Keuangan · Rekonsiliasi Shift · Alerts · Audit Ledger viewer | 5 halaman selesai |

**Gerbang keluar minggu 3:** seluruh metode bayar berfungsi (EDC via simulator) · anti-passback
terbukti dengan tes · rantai audit terverifikasi · 9 dari 12 halaman berjalan di atas data nyata.

#### Minggu 4 (17–21 Agustus) — Sync, Multi-Tenant, QA

| Dev | Pekerjaan | Output |
|-----|-----------|--------|
| A | Sync agent (outbox → Cloud gRPC) · Cloud receiver + idempotensi · **chaos test**: cabut jaringan, matikan PC saat transaksi, kertas habis, controller offline · deploy Cloudflare Tunnel | Offline-first terbukti |
| B | Isolasi multi-tenant + tes keamanan · bulk CSV import/export · Hardware Config + Field Monitor · polish visualisasi | 12 halaman selesai |
| Keduanya | **QA intensif** (sesuai anggaran 1 fase) · UAT · dokumentasi serah terima · runbook deployment | Rilis Fase 1 |

**Gerbang keluar minggu 4 = kriteria rilis:** lihat §19.

### 16.4 Fase 1b — Integrasi Lapangan (di luar 4 minggu)

Jendela ini **tidak dapat dijadwalkan oleh kita** karena bergantung pihak ketiga:

| Aktivitas | Prasyarat | Estimasi |
|-----------|-----------|----------|
| Integrasi controller lapangan nyata | Firmware tim lapangan siap sesuai §5.3 | 2–3 hari |
| Integrasi EDC/JTMO fisik | Unit EDC + SDK + kredensial dari acquirer | 3–5 hari |
| Kalibrasi LPR di lokasi | Kamera & pencahayaan terpasang | 2–3 hari |
| UAT lapangan + go-live | Semua di atas | 2 hari |

**Karena seluruh logika sudah tervalidasi di atas simulator, jendela ini murni adalah menyambungkan
implementasi konkret ke antarmuka yang sudah ada** — bukan menulis logika baru. Inilah alasan
prinsip P7 (semua perangkat punya simulator) menjadi jalur kritis, bukan kemewahan.

---

## 17. SCOPE YANG DITURUNKAN KE FASE 2

Daftar ini adalah konsekuensi langsung dari aritmatika §16.1. Setiap item disertai alasan agar dapat
didiskusikan dengan klien secara terbuka.

| # | Item | Hemat | Alasan | Mitigasi di Fase 1 |
|---|------|------:|--------|---------------------|
| 1 | **Integrasi EDC/JTMO fisik** | 5 hari | Bergantung ketersediaan unit EDC, SDK vendor, dan sertifikasi acquirer — **di luar kendali kita** | Interface + adapter simulator selesai; integrasi konkret = 1 file baru |
| 2 | **Firmware controller nyata** | 4 hari | Ditangani tim lapangan (§1.3) | Protokol §5.3 + simulator lengkap sebagai kontrak |
| 3 | **Penyetelan akurasi LPR** | 4 hari | Butuh data lapangan nyata; tidak bisa disetel tanpa foto dari lokasi | Model baseline + `ocr_logs` untuk mengumpulkan data penyetelan |
| 4 | **Notifikasi email/WhatsApp** | 2 hari | Tidak ada di anggaran OpEx | Alert dalam aplikasi + badge real-time |
| 5 | **Laporan PDF terjadwal** | 2 hari | Ekspor CSV/XLSX sudah memenuhi kebutuhan inti | Ekspor manual tersedia |
| 6 | **Redis + rate limiting terdistribusi** | 2 hari | Tidak ada di anggaran OpEx | In-process cache; cloud-api single instance |
| 7 | **HA PostgreSQL / replika baca** | 3 hari | Tidak ada di anggaran | Backup harian + PITR; Edge tetap jalan saat Cloud down |
| 8 | **Aplikasi mobile / portal pelanggan** | — | Tidak pernah masuk scope | — |
| 9 | **Prabayar & top-up saldo member** | 3 hari | Tidak ada di scope revisi | Member = tarif Rp0 atau tagih casual |
| 10 | **Fase QA kedua** | — | Anggaran QA dipangkas Rp10jt → Rp5jt | Satu fase QA intensif; **risiko diterima klien secara eksplisit** |
| **Total dihemat** | | **25 hari** | | |

> **Rekomendasi:** dokumen revisi sendiri menulis bahwa pemangkasan QA *"direkomendasikan untuk
> ditinjau ulang bila anggaran bertambah"*. Kami menguatkan rekomendasi itu. Dari 65 dev-day
> pekerjaan yang dipadatkan ke 40, fase QA kedua adalah tempat pertama yang harus dipulihkan jika
> ada tambahan anggaran — bukan fitur baru.

---

## 18. RISIKO, ASUMSI & OPEN QUESTIONS

### 18.1 Risiko Utama

| # | Risiko | Dampak | Peluang | Mitigasi |
|---|--------|--------|---------|----------|
| R1 | **Definisi "JTMO" belum jelas** | Tinggi — item Rp12jt, terbesar di New Scope | Tinggi | Adapter vendor-agnostik; **butuh jawaban dalam 3 hari** agar tidak memblokir minggu 3 |
| R2 | **Unit EDC & SDK tidak tersedia dalam 4 minggu** | Tinggi | Tinggi | Adapter simulator; integrasi konkret masuk Fase 1b |
| R3 | **Firmware lapangan tidak sesuai §5.3** | Tinggi | Sedang | Kirim spesifikasi protokol di **hari 1**, minta konfirmasi tertulis, sediakan simulator untuk mereka uji |
| R4 | **Timeline 4 minggu vs 65 hari kerja** | Tinggi | **Terjadi** | §17 memindahkan 25 hari; tidak ada buffer tersisa — setiap penambahan scope memerlukan pengurangan yang setara |
| R5 | **Akurasi LPR di plat Indonesia** | Sedang | Tinggi | LPR bukan gerbang keputusan (§7.3); log OCR untuk perbaikan |
| R6 | **QA satu fase** | Sedang | Sedang | Otomasi tes untuk fare engine, audit chain, dan state machine sejak minggu 1 |
| R7 | **Selisih anggaran Rp59jt vs Rp54jt** | Sedang | **Terjadi** | Klarifikasi sebelum kontrak (§2.3) |
| R8 | **Cloud single instance tanpa HA** | Rendah | Sedang | Edge offline-first membatasi dampak; backup + PITR |
| R9 | **Sertifikasi acquirer memakan minggu** | Sedang | Sedang | Mulai proses administrasi paralel sejak minggu 1, jangan tunggu development selesai |
| R10 | **17 Agustus libur nasional → defisit 2 dev-day** | Sedang | **Terjadi** | Pilih opsi A/B/C di §16.2 sebelum mulai |

### 18.2 Asumsi yang Perlu Divalidasi

1. Tim lapangan bersedia mengimplementasikan protokol §5.3 (bukan protokol proprietary vendor palang).
2. Deployment pertama adalah **satu site**, dengan satu gerbang masuk dan satu gerbang keluar.
3. Edge Node adalah PC Windows/Linux dengan port serial (atau konverter USB-serial) dan UPS.
4. Kamera IP mendukung RTSP dan sudah terpasang dengan pencahayaan memadai.
5. Payment gateway yang dipilih adalah Midtrans **atau** Xendit (satu, bukan keduanya).
6. Jenis kendaraan tetap 4 kategori: mobil, motor, truk, bus.
7. Kasir menjaga gerbang keluar (bukan mode nirawak penuh di Fase 1).
8. Klien menyediakan kredensial PG dan akun DigitalOcean sebelum minggu 3.

### 18.3 Open Questions — Butuh Jawaban Klien

| # | Pertanyaan | Memblokir | Batas waktu |
|---|------------|-----------|-------------|
| Q1 | **Apa definisi tepat "JTMO"?** Vendor spesifik, atau istilah umum untuk e-Toll/e-Money? | Modul pembayaran (Rp12jt) | Hari 3 |
| Q2 | Merek & model EDC yang akan dipakai? Ada dokumen protokolnya? | Adapter EDC | Hari 5 |
| Q3 | Midtrans atau Xendit? Akun sudah ada? | QRIS/e-wallet | Minggu 2 |
| Q4 | Total anggaran Rp54jt atau Rp59jt? (§2.3) | Kontrak | Sebelum mulai |
| Q5 | Berapa site di deployment pertama? Berapa gerbang per site? | Perencanaan kapasitas | Hari 3 |
| Q6 | Tarif nyata & aturan peak hour di lokasi? | Fare engine | Minggu 2 |
| Q7 | Merek palang & controller yang sudah terpasang? Protokolnya bisa diubah? | Kontrak §5.3 | **Hari 1** |
| Q8 | Ada UPS di lokasi? Kalau tidak, target 99,99% harus direvisi | `NFR-2.1` | Minggu 1 |
| Q9 | Berapa lama data transaksi & foto wajib disimpan (regulasi/kebutuhan klien)? | Kebijakan retensi | Minggu 2 |
| Q10 | Siapa PIC tim lapangan untuk koordinasi protokol? | Semua integrasi hardware | **Hari 1** |

---

## 19. DEFINITION OF DONE

### 19.1 Kriteria Rilis Fase 1

Rilis dinyatakan siap hanya jika **seluruh** butir berikut terpenuhi:

**Fungsional**
- [ ] Kendaraan casual dapat masuk: LD1 → LPR → tombol → tiket tercetak ≤ 1s → palang buka → tercatat
- [ ] Member dapat masuk dengan tap RFID; anti-passback menolak tap masuk kedua
- [ ] Kendaraan dapat keluar: identifikasi (QR/RFID/plat) → komparasi foto → tarif benar → bayar → palang buka
- [ ] Seluruh 6 metode bayar berfungsi (EDC via simulator jika unit fisik belum ada)
- [ ] Anti-tailgating terbukti: mobil kedua tidak dapat lewat dengan satu tiket
- [ ] Interlock keselamatan terbukti: palang tidak menutup saat loop bawah HIGH
- [ ] 12 halaman dashboard berjalan di atas PostgreSQL, bukan mock
- [ ] RBAC berfungsi untuk ketiga peran, diverifikasi di backend
- [ ] Bulk CSV import (dengan pratinjau) & export berfungsi
- [ ] Rantai audit terverifikasi; perusakan data terdeteksi

**Non-fungsional**
- [ ] p95 latensi LPR ≤ 1.000 ms (dibuktikan dari `ocr_logs`, bukan asumsi)
- [ ] Cetak tiket ≤ 1.000 ms
- [ ] Update POS < 500 ms setelah settlement
- [ ] **Tes offline 24 jam: nol transaksi hilang, seluruh antrean tersinkronisasi saat online**
- [ ] Restart `edge-api` di tengah transaksi tidak merusak data
- [ ] Isolasi tenant terverifikasi (tenant A tidak dapat membaca data tenant B)
- [ ] Nol kredensial hardcoded (diverifikasi dengan pemindaian)
- [ ] Seluruh layanan dalam Docker dengan restart policy `always`

**Serah terima**
- [ ] Spesifikasi protokol §5.3 dikirim & dikonfirmasi tim lapangan
- [ ] Runbook deployment
- [ ] Panduan troubleshooting perangkat
- [ ] Manual pengguna (kasir & admin)
- [ ] Prosedur backup & restore teruji (restore benar-benar dicoba, bukan hanya backup)
- [ ] Kredensial diserahkan lewat kanal aman

### 19.2 Definition of Done per Task

Setiap task dianggap selesai bila: kode ter-review · lint bersih (`golangci-lint`, ESLint) · unit
test untuk logika bisnis · di-tes terhadap simulator · tag `FR-x.y` dirujuk di commit · dokumentasi
diperbarui bila kontrak berubah · **tanpa TODO yang menggantung di jalur kritis**.

---

## LAMPIRAN A — RINGKASAN KEPUTUSAN DESAIN

| # | Keputusan | Alasan |
|---|-----------|--------|
| D1 | Rantai audit per-node, bukan global | Rantai global mustahil dikoordinasikan saat offline |
| D2 | LPR bukan gerbang keputusan | False-positive OCR lebih merusak daripada plat tak terbaca |
| D3 | Member tetap bisa masuk saat kertas habis | Memutus akses penghuni karena printer adalah kegagalan tak perlu |
| D4 | Transactional outbox, bukan message queue | Menjamin atomisitas dengan data bisnis; nol infrastruktur tambahan |
| D5 | Tarif versioned, bukan di-UPDATE | Transaksi lama harus dapat direkalkulasi dengan tarif saat itu |
| D6 | Uang sebagai `BIGINT` rupiah, bukan float | Pembulatan float pada uang adalah bug yang menunggu terjadi |
| D7 | Kredensial PG hanya di Cloud | Edge Node berada di lapangan, lebih rentan secara fisik |
| D8 | Timeout EDC ≠ gagal, harus query batch | Double-charge jauh lebih mahal daripada palang telat 5 detik |
| D9 | Simulator untuk seluruh perangkat | Jalur kritis dengan timeline 4 minggu & hardware belum pasti |
| D10 | `tenant_id` sejak baris pertama | Menambahkannya belakangan berarti migrasi data yang menyakitkan |
| D11 | Interlock keselamatan redundan (PC + firmware) | Keselamatan fisik tidak boleh bergantung satu titik |
| D12 | Feature flag mock dipertahankan | Kemampuan demo tanpa backend terlalu berharga untuk dibuang |

---

## LAMPIRAN B — REFERENSI

- Dokumen scope: `New_Scope_Parking_System_Revisi.docx` (Konsolidasi Core MVP + New Scope)
- PRD sebelumnya: `Jabar-Creative/Bangun-Parkir-Mandiri` → `PRD.md` v1.0.0
- Panduan kontributor: `CLAUDE.md`
- Prototype UI: `apps/mvp-demo` (React 19 + TS + Tailwind v4 + Zustand)
- Catatan prototype: `docs/prototype-notes.md`

---

*Dokumen ini adalah pondasi, bukan kontrak final. Bagian §18.3 (Open Questions) harus terjawab
sebelum estimasi dapat dikunci — khususnya Q1, Q7, dan Q10 yang memblokir jalur kritis sejak hari
pertama.*

