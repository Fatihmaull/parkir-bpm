# PRD v3.0.0 — Arsitektur Enterprise, Hardware & Zero-Downtime

**Versi:** 3.0.0 (Enterprise / Hardware-Integrated)
**Menggantikan sebagai source-of-truth arsitektur:** [`PRD_PONDASI.md`](PRD_PONDASI.md) v2.0.0
**Basis hardware baru:** `Spesifikasi Protokol Komunikasi TCP Controller v1.0` (Port 56001)
**Basis topologi:** diagram klien (3-tier: Device → PC Admin per Lahan → Server & Dashboard Pusat)

> **Cakupan v3.** v2 tetap berlaku untuk **detail transaksional** yang tak berubah (fare engine §5.5.2,
> rantai audit §9.1, model tarif versioned, RBAC dasar, keamanan §14). v3 **menggantikan & memperluas**:
> topologi deployment, kontrak hardware, logika/software tiap perangkat, model multi-gerbang, dan
> strategi zero-downtime. Bila v3 dan v2 bertentangan pada tiga hal itu, **v3 menang**.

---

## 1. Perubahan Model Deployment (3-Tier)

Diagram klien menegaskan hierarki **tiga lapis**. Ini menjadi tulang punggung arsitektur v3.

```
TIER 3 — PUSAT (Cloud/Server Utama)
  └─ Dashboard & Server Utama + DB pusat (agregasi semua lahan, multi-tenant)
        ▲  Connect via internet (outbound dari tiap lahan; Cloud tak pernah jadi dependensi runtime)
        │
TIER 2 — LAHAN PARKIR (Edge / "PC Admin")   ← 1 per lahan (N lahan)
  ├─ PC Admin: edge-api + PostgreSQL lokal + POS + monitor lokal
  │   otak seluruh keputusan lahan; menyimpan data lalu meneruskan ke Pusat
        ▲  LAN (Ethernet lokal)
        │
TIER 1 — GERBANG (Device Controller)         ← 1 controller per gerbang
  ├─ Gate Masuk 1..N : [Palang & Detektor (TCP A6/A9)] + [Mesin Tiket Otomatis]
  └─ Gate Keluar 1..N: [Palang & Detektor (TCP A6/A9)] + [PC Kasir + Scanner]
```

**Pemetaan ke diagram klien:**
- "device client" + "dashboard dan server utama" = **Tier 3 (Pusat)**.
- "Lahan Parkir 1/2/3" masing-masing punya **PC admin** = **Tier 2 (Edge Node)**.
- Tiap lahan punya **≥2 gate masuk + ≥2 gate keluar**; tiap gate berisi **Palang & Detektor** (controller TCP) dan **Mesin Tiket** (masuk) atau **PC Kasir + scanner** (keluar) = **Tier 1**.

**Perbedaan besar dari v2:** v2 mengasumsikan 1 site = 1 gerbang masuk + 1 gerbang keluar dan satu
`edge-api` tunggal. **v3: satu Edge (PC Admin) per lahan mengelola BANYAK gerbang** (2 masuk + 2 keluar,
dapat bertambah), tiap gerbang = satu **device controller TCP** independen.

---

## 2. Hierarki Entitas & Terminologi

| Istilah diagram | Istilah sistem | Tabel |
|-----------------|----------------|-------|
| — (organisasi) | Tenant | `tenants` |
| Lahan Parkir | Site | `sites` |
| PC Admin | Edge Node (1 per site) | `sites.config` / `nodes` (baru) |
| Gate masuk/keluar | Gate | `gates` (sudah ada: `kind=ENTRY|EXIT`, N per site) |
| Palang & Detektor | Device controller (BARRIER+LOOP+LIGHT+RFID) | `devices` |
| Mesin Tiket Otomatis | Device PRINTER | `devices` |
| PC Kasir + scanner | POS terminal + scanner | `devices` (SCANNER) + POS app |

> Skema `gates`/`devices` **sudah mendukung** N gerbang per site & peripheral per gate. Yang perlu:
> generalisasi runtime Edge dari "1 masuk + 1 keluar" ke "N controller".

---

## 3. Prinsip Arsitektur (v2 P1–P7 + penegasan Zero-Downtime)

P1–P7 dari [`PRD_PONDASI.md`](PRD_PONDASI.md) §3 tetap mengikat. v3 menambah/menajamkan:

**P8 — Zero-downtime operasional per lahan.**
Karena hardware berjalan di **LAN lokal**, operasi gerbang **tidak boleh** bergantung internet maupun
Pusat. Titik kegagalan tunggal yang tersisa adalah **Edge (PC Admin)** — maka Edge wajib: proses
auto-restart (service/daemon), watchdog, DB lokal durable, pemulihan < 15 dtk (NFR-2.3), dan UPS
(pengadaan klien). Controller Tier-1 "bodoh" (relay), jadi otak ada di Edge; ketahanan Edge = ketahanan
lahan.

**P9 — Controller tak dipercaya untuk keselamatan (turunan P3/P4).**
Controller A6/A9 **tidak** menyediakan interlock, heartbeat, debounce, atau auto-close. Semua aturan
keselamatan (P4) & timing **ditegakkan di Edge** (untuk sekarang; redundansi firmware = fase lanjut).

**Tingkatan ketersediaan (availability tiers):**

| Yang mati | Dampak | Perilaku |
|-----------|--------|----------|
| Internet / Pusat | Dashboard pusat basi | Lahan **jalan penuh**; outbox menumpuk, sync saat online (P1) |
| Satu controller gerbang | 1 gerbang berhenti | Gerbang lain jalan; alert `critical`; petugas manual |
| PC Admin (Edge) | **Seluruh lahan berhenti** | Auto-restart + watchdog; UPS; recovery < 15 dtk |
| Printer tiket habis | Gerbang masuk casual berhenti | **Member tetap masuk** (RFID), lampu kuning, alert (D3) |
| LPR/kamera | Plat `UNREAD` | Kendaraan tetap dilayani (P2) |

---

## 4. Arsitektur Sistem v3

### 4.1 Tier-1 — Device Controller (per gerbang)
- **Palang & Detektor** = controller Ethernet **4-input / 4-output relay + Wiegand**, protokol
  **A6/A9-TCP** (§5). Device = **TCP server** (mis. `192.168.1.56:56001`); Edge = client.
- **Mesin Tiket Otomatis** (gerbang masuk) = printer thermal + dispenser tiket + tombol. Perangkat
  **terpisah**, protokol **belum ditentukan** (open question §13) — kemungkinan USB/serial ESC/POS
  atau TCP tersendiri. Diabstraksi sebagai `TicketPrinter`.
- **PC Kasir + Scanner** (gerbang keluar) = PC menjalankan POS web + **scanner QR/barcode** (USB-HID
  keyboard-wedge atau serial). Scanner dibaca aplikasi POS, bukan controller A6/A9.

### 4.2 Tier-2 — Edge Node (PC Admin, 1 per lahan)
- `edge-api` (Go): **manajer multi-gerbang** — satu goroutine pemilik per device (§5.6 v2), state
  machine per gerbang, fare engine, audit chain, WS untuk POS & monitor lokal, sync agent.
- PostgreSQL lokal + outbox.
- POS web (per gerbang keluar) + Monitor Lahan (semua gerbang lahan itu) — **dashboard lokal**.
- lpr-svc (opsional) per lahan.

### 4.3 Tier-3 — Pusat (Cloud/Server Utama)
- `cloud-api` (Go): sync receiver (idempoten, per node), agregasi lintas-lahan, dashboard API, auth,
  verifikasi rantai audit.
- DB pusat (managed PostgreSQL), object storage snapshot.
- **Dashboard Pusat** (React): lintas-lahan, multi-tenant, read-mostly.

### 4.4 Konektivitas
- Tier1↔Tier2: **LAN Ethernet**, TCP. Edge selalu memelihara koneksi ke tiap controller (reconnect,
  keepalive PING — §6.1).
- Tier2→Tier3: **internet outbound** (Cloudflare Tunnel), sync batch + webhook PG masuk via Pusat.
- Aturan: kontrol palang & pembayaran **tidak pernah** lewat internet (P1).

---

## 5. Kontrak Hardware — Protokol A6/A9-TCP (formalisasi dari spec vendor v1.0)

### 5.1 Transport
- Device = **TCP server** per gerbang. Alamat per gerbang di `gates.endpoint` (`ip:port`, default
  `:56001`). Edge = **client**, satu koneksi persisten per device.

### 5.2 Frame
```
Header(0xA6) + Command(ASCII) [+ Data(ASCII)] + Footer(0xA9)
```
- Tanpa LEN, tanpa CRC, tanpa address, tanpa byte-stuffing (byte kontrol 0xA6/0xA9 > 127, ASCII < 128 →
  tak bentrok). Integritas mengandalkan TCP. Frame valid hanya bila **Header & Footer lengkap**.

### 5.3 Perintah Host→Device (tiap dibalas `…OK`)
`OUT1ON..OUT4ON`, `OUT1OFF..OUT4OFF`, `TRIG1..TRIG4` (relay ON 1 dtk lalu OFF), `PING`→`PINGOK`,
`STAT`→`STATabcdefgh`.

### 5.4 Event tak-diminta Device→Host
- `IN1ON/OFF … IN4ON/OFF` — perubahan input (loop/tombol). **Asumsi belum ter-debounce** → Edge
  wajib debounce (§6.1).
- `Wxxxxxx` — Wiegand RFID; `xxxxxx` = 6 hex (W-26) atau 8 hex (W-34, auto-detect).

### 5.5 STAT
`STATabcdefgh`: a–d = Input1–4, e–h = Output1–4, nilai `1`/`0`. **Konfirmasi ke vendor** semantik &
apakah kaya-fitur (open question §13 — klien menyatakan "format STAT belum pasti").

### 5.6 Peta Pin (KEPUTUSAN DESAIN v3 — default, dapat dikonfigurasi via `gates.config`)

**Gerbang MASUK** (1 controller):
| Kanal | Fungsi |
|-------|--------|
| `IN1` | LD1 — loop kehadiran sebelum palang (trigger LPR + aktifkan tombol/RFID) |
| `IN2` | LD2 — loop di bawah palang (interlock + konfirmasi kendaraan lewat) |
| `IN3` | Tombol ambil tiket |
| `IN4` | Cadangan (sensor tamper / pintu box) |
| `Wiegand` | RFID reader member |
| `OUT1` | Palang (naik/turun) |
| `OUT2` | Lampu hijau |
| `OUT3` | Lampu merah |
| `OUT4` | Buzzer / cadangan |

**Gerbang KELUAR** (1 controller):
| Kanal | Fungsi |
|-------|--------|
| `IN1` | LD3 — loop kehadiran sebelum palang keluar |
| `IN2` | LD4 — loop di bawah palang keluar (interlock + lewat) |
| `IN3` | Cadangan |
| `IN4` | Cadangan |
| `Wiegand` | RFID reader member (tap keluar) |
| `OUT1` | Palang keluar |
| `OUT2` | Lampu hijau |
| `OUT3` | Lampu merah |
| `OUT4` | Cadangan |

Scanner QR tiket di gerbang keluar dibaca **PC Kasir** (bukan controller).

---

## 6. Software & Logika per Perangkat

### 6.1 Gate Controller Driver (Layer-1, `internal/hardware/tcpctl`)
Klien TCP A6/A9 dengan **best-practice keandalan** (jawaban "device terus terkoneksi"):
- **Koneksi persisten** per device; **auto-reconnect** dengan exponential backoff + jitter
  (mis. 0.5s→1s→2s→5s→maks 15s).
- **Keepalive**: kirim `PING` tiap **1 dtk**; tandai device `OFFLINE` bila 3× tanpa `PINGOK` (3 dtk).
  Aktifkan TCP keep-alive socket.
- **Watchdog per device**: bila OFFLINE → state machine gerbang → `FAULT`, lampu merah (perintah
  terakhir yang berhasil), alert `critical`; saat pulih → resync `STAT`.
- **Framing parser**: akumulasi byte, potong per Header..Footer; abaikan frame tak lengkap.
- **Debounce input** ≥150 ms di Edge (karena controller tak menjamin) sebelum diteruskan ke state
  machine sebagai `LoopEvent`.
- **Korelasi perintah↔respons** sederhana (protokol ini serial per-koneksi; kirim `OUT1ON`, tunggu
  `OUT1ONOK` dengan timeout + retry 3×).
- **Telemetry ring buffer** TX/RX (untuk halaman Hardware Config), audit bila `severity ≥ warning`.

Mengimplementasikan interface Layer-2 v2 (`Barrier`, `LoopDetector`, `IndicatorLight`, `RFIDReader`)
sehingga **state machine gerbang tidak berubah** — hanya driver baru.

### 6.2 Logika Palang (jawaban: buka tertrigger, tutup oleh sensor saat lewat)
- **Buka**: Edge kirim `OUT1ON` (palang naik) saat otorisasi (tiket diambil / member valid). Lampu
  hijau (`OUT2ON`, `OUT3OFF`).
- **Tutup**: Edge kirim `OUT1OFF` **hanya setelah** kendaraan lewat — dideteksi **LD2/LD4 rising lalu
  falling** (kendaraan melewati loop bawah lalu keluar loop). Lampu merah (`OUT3ON`).
- **Interlock keselamatan (P4, murni di Edge)**: Edge **DILARANG** mengirim `OUT1OFF` selama LD2/LD4 =
  HIGH (kendaraan masih di bawah palang). Tunggu falling.
- **Anti-tailgating (§5.4.1 v2)**: bila saat menutup LD1/LD3 masih HIGH → mulai siklus baru untuk
  kendaraan kedua.
- **Pengaman waktu (di Edge, ganti fungsi firmware v2)**: auto-close bila palang terbuka > 60 dtk &
  loop bawah LOW; alert bila LD2/LD4 HIGH > 120 dtk (`BARRIER_BLOCKED`), atau LD naik tanpa otorisasi
  (`UNAUTHORIZED_PASSAGE`).

> Alternatif `TRIG1` (pulsa 1 dtk lalu OFF sendiri) tersedia bila palang tipe "pulse-to-open,
> auto-close-mekanis". Mode (`hold` OUT1ON/OFF vs `pulse` TRIG1) di-konfig per gerbang di
> `gates.config.barrier_mode`. Default: **hold** (agar Edge kontrol penuh timing tutup via sensor).

### 6.3 State Machine Gerbang
Tetap seperti v2 §5.4 (masuk) & §5.5 (keluar) — sudah teruji di simulator. Yang berubah hanya
**sumber sinyal** (driver A6/A9, bukan STX/CRC) dan **penegakan interlock di Edge**.

### 6.4 Mesin Tiket Otomatis (`TicketPrinter`)
- Cetak tiket (kode + QR) saat state `ISSUING`. Deteksi status kertas (OK/menipis/habis/jam) →
  degradasi (D3: kertas habis → casual berhenti, member tetap masuk).
- **Protokol belum pasti** (§13) — abstraksi `TicketPrinter` sudah ada; adapter konkret menyusul saat
  merek printer diketahui.

### 6.5 PC Kasir + Scanner (gerbang keluar)
- POS web lokal (Tier-2) + scanner QR/barcode (USB-HID keyboard-wedge → input field, atau serial).
- Alur keluar v2 §5.5: scan tiket → lookup → komparasi foto → tarif → bayar → `OUT1ON`.
- Metode bayar offline (tunai/EDC/member) jalan tanpa internet; QRIS/e-wallet nonaktif saat offline.

### 6.6 Manajer Multi-Gerbang (Edge, `gatesvc` diperluas)
- Muat daftar `gates` milik site dari DB → untuk tiap gate, buat driver (transport `tcp`/`sim`) +
  controller + goroutine pemilik. Skala N gerbang (bukan hardcode 1+1).
- Bus WS lahan menyiarkan event **berlabel `gate_code`** agar Monitor Lahan menampilkan banyak gerbang.

### 6.7 Wiegand RFID
- Parse `Wxxxxxx`: 6 hex → W-26, 8 hex → W-34. Normalisasi ke `rfid_uid` (uppercase hex). Feed ke
  validasi member (anti-passback §8.2 v2).

---

## 7. Zero-Downtime & Keandalan (P8/P9)

- **Local-first mutlak**: keputusan gerbang & pembayaran hanya butuh Edge + controller LAN.
- **Edge sebagai layanan**: jalankan `edge-api` sebagai service Windows/systemd `restart=always`;
  **watchdog eksternal** (mis. NSSM/systemd) restart bila crash; healthcheck internal.
- **Recovery cepat (NFR-2.3)**: state operasional dari DB lokal; saat restart, resync `STAT` tiap
  controller untuk merekonstruksi status loop/output.
- **Koneksi controller**: reconnect + keepalive (§6.1). Antrian perintah per device agar tak hilang
  saat blip.
- **Degradasi**: matriks §3 (availability tiers) + matriks v2 §5.4.3.
- **Data**: outbox transaksional (§10 v2) → nol kehilangan saat internet putus (NFR-2.2), sudah
  terbukti e2e.
- **HA lanjutan (opsional, pengadaan)**: UPS wajib; Edge cadangan hot-standby (fase lanjut); DB lokal
  backup harian.

---

## 8. Dashboard — Pusat vs Lokal, Kesesuaian dengan Diagram

Diagram menyiratkan **dua konteks dashboard**:
1. **Dashboard Lokal (PC Admin / Tier-2)** — operasional satu lahan: Field/Gate Monitor semua gerbang
   lahan itu, POS kasir, Hardware Config, alerts lokal. Sumber data: `edge-api` lokal (LAN, real-time WS).
2. **Dashboard Pusat (Tier-3)** — lintas-lahan & multi-tenant: agregasi keuangan/volume, status semua
   lahan, audit ledger, membership global. Sumber data: `cloud-api` (agregat ter-sync).

**Kesesuaian dashboard kita saat ini:** *sebagian sesuai.*
- ✅ Konsep 3-tier (edge→cloud), 12 halaman, RBAC, tema, login — cocok.
- ⚠️ **Perlu dipisah** peran Pusat vs Lokal (sekarang satu app langsung ke edge; Pusat harus ke cloud).
- ⚠️ **Multi-gerbang**: Field Monitor & Hardware Config harus menampilkan **N gerbang per lahan**
  (sekarang 1 masuk + 1 keluar).
- ⚠️ **Pemilih Lahan**: TopBar "site" harus mencerminkan hierarki Lahan; Pusat = "Semua Lahan" agregat.
- RBAC & 12 halaman (§12 v2) dipertahankan; ditambah konteks tier.

---

## 9. Delta Model Data
- `gates`: sudah mendukung N per site (`kind ENTRY|EXIT`, `endpoint ip:port`, `config` untuk pin map &
  `barrier_mode`). **Tak perlu migrasi struktur**, cukup diisi > 1 baris/site.
- `devices`: tambah `SCANNER` ke enum `kind`; simpan pin map & tipe di `config`.
- Tambah tabel `nodes` (opsional): 1 Edge per site (`site_id`, `node_id`, `last_seen_at`, versi) untuk
  monitor kesehatan lahan di Pusat.
- Event WS & audit membawa `gate_code` (bukan sekadar entry/exit) untuk N gerbang.
- Sisanya (payments, vehicles_log, ocr_logs, audit_logs, sync_outbox) tetap v2 §11.

---

## 10. Keamanan & RBAC
Tetap v2 §14 (JWT/Argon2id, tenant isolation, PAN masked, kredensial PG hanya di Cloud). Tambahan:
- Akses admin ke Edge via Cloudflare Access (bukan port publik).
- Otentikasi antar-tier (Edge→Cloud) mTLS per node.
- Kredensial controller (jika ada) & konfigurasi jaringan via `.env`/secret, bukan hardcoded.

---

## 11. Open Questions (v3)
| # | Pertanyaan | Memblokir |
|---|-----------|-----------|
| H1 | **Protokol Mesin Tiket Otomatis** (merek/printer, ESC/POS? USB/serial/TCP?) | Adapter printer |
| H2 | **Semantik STAT** & apakah kaya-fitur (klien: "belum tahu") | Rekonstruksi status saat reconnect |
| H3 | Model palang: `hold` (OUT ON/OFF) vs `pulse` (TRIG)? Palang auto-close mekanis? | Logika tutup §6.2 |
| H4 | Scanner keluar: USB-HID wedge atau serial? Merek? | POS scanner |
| H5 | 1 controller benar-benar 1 gerbang? IP tiap controller statis? | Peta jaringan lahan |
| H6 | Kamera LPR per gerbang: RTSP tersedia? | LPR per gerbang |
| H7 | Sisa Q1/Q4/Q7/Q10 v2 (JTMO, anggaran, PIC lapangan) | Kontrak/pembayaran |
