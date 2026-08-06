# Rangkuman Perubahan — Implementasi v2 → Refine v3

Ringkas: apa yang **sudah** kita bangun (mengacu PRD v2), dan apa yang **berubah/bertambah** setelah
refine besar v3 (topologi 3-tier klien + hardware controller A6/A9-TCP nyata).

## A. Yang sudah ada & tetap dipakai (tak berubah)
- **State machine gerbang masuk & keluar** (§5.4/§5.5) — teruji end-to-end di simulator.
- **Fare engine** (§5.5.2), **rantai audit SHA-256** (§9), **anti-passback** (§8.2).
- **Auth JWT/Argon2id + RBAC + isolasi multi-tenant** (§14/§12.14).
- **Transactional outbox + sync agent + cloud receiver** (§10) — offline-first terbukti e2e.
- **CRON 6 job**, **LPR degradasi + ocr_logs**, **QRIS webhook (signature+idempotensi)**.
- **12 halaman dashboard** + login + tema + charts.
- **CI/CD** (GitHub Actions + image GHCR).
- Skema `gates`/`devices` — **ternyata sudah** mendukung N gerbang per site & peripheral per gate.

## B. Perubahan konseptual besar (v3)
| # | v2 | v3 | Alasan |
|---|----|----|--------|
| 1 | 1 site = 1 gerbang masuk + 1 keluar; 1 `edge-api` | **1 Edge (PC Admin) per lahan mengelola N gerbang** (2 masuk + 2 keluar, dst.) | Diagram klien |
| 2 | Kontrak protokol **buatan kita** (STX/ADDR/LEN/CRC16/ETX + stuffing) | **Protokol vendor A6/A9-TCP** (Header/Footer, ASCII, tanpa CRC/LEN/address) | Hardware nyata sudah ada standarnya |
| 3 | Transport utama **RS232** | **TCP/IP LAN**; device = **TCP server**, Edge = client | Spec vendor |
| 4 | Interlock/heartbeat/debounce/auto-close **wajib di firmware** (redundan) | Controller "bodoh" → semua **ditegakkan di Edge** (P9) | Device tak menyediakannya |
| 5 | Palang buka/tutup generik | **Buka via perintah, tutup dipicu sensor** saat kendaraan lewat (LD falling) + interlock Edge | Jawaban klien |
| 6 | Dashboard tunggal | **Dua konteks: Dashboard Pusat (cloud) vs Lokal (PC Admin)** | Topologi 3-tier |
| 7 | Zero-downtime implisit | **P8 eksplisit**: lahan jalan penuh tanpa internet; Edge = titik kritis → service+watchdog+UPS | Hardware LAN lokal |

## C. Yang perlu DIBANGUN/DIUBAH di kode (delta implementasi)
1. **Driver hardware baru** `internal/hardware/tcpctl` — klien A6/A9-TCP: framing parser, reconnect+backoff,
   keepalive PING, debounce input, korelasi cmd/resp, telemetry. Mengimplementasikan interface Layer-2 yang
   **sudah ada** (Barrier/Loop/Light/RFID) → state machine tak berubah.
2. **Generalisasi `gatesvc`** dari hardcode 1+1 menjadi **manajer N gerbang** (muat dari `gates`, goroutine
   pemilik per device, event WS berlabel `gate_code`).
3. **Interlock & timer keselamatan pindah ke Edge** (auto-close 60s, BARRIER_BLOCKED, UNAUTHORIZED_PASSAGE) —
   sebagian sudah di state machine; lengkapi timer.
4. **Peta pin per gerbang** (§5.6 v3) di `gates.config`; mode palang `hold|pulse`.
5. **Adapter Mesin Tiket** konkret (menunggu H1) — abstraksi sudah ada.
6. **Scanner POS** di gerbang keluar (USB-HID/serial) — komponen input POS.
7. **Pemisahan Dashboard Pusat vs Lokal**: Pusat → `cloud-api` (agregat); Lokal → `edge-api` (real-time).
8. **Field Monitor & Hardware Config multi-gerbang** (tampilkan N gerbang per lahan).
9. **Pemilih Lahan** hierarkis; Pusat "Semua Lahan".
10. **Tabel `nodes`** (opsional) untuk kesehatan Edge per lahan di Pusat.
11. **Ketahanan Edge**: paket sebagai service + watchdog + resync STAT saat reconnect.
12. Enum `devices.kind` + `SCANNER`.

## D. Yang TIDAK berubah dan tak perlu disentuh
Fare engine, rantai audit, outbox/sync, auth, tenant isolation, model tarif versioned, CRON, webhook PG,
uang BIGINT, PAN masked. Semua tetap berlaku sebagaimana v2.

## E. Blocker yang masih relevan
- Docker/pgx (repository nyata) — belum bisa validasi migrasi.
- H1 (protokol printer), H2 (STAT), H4 (scanner) — detail hardware menunggu info vendor/klien.
- Q1/Q4/Q7/Q10 v2 (JTMO, anggaran, kontrak, PIC lapangan).
