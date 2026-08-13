# Tasking — Menuju Enterprise-Ready (2 Programmer)

Dipecah per **epik**, tanpa timeline. Tujuan: melihat total lingkup kerja membawa app ke siap-produksi.
**Dev A** = Edge / hardware / pembayaran / sync. **Dev B** = Cloud / dashboard / POS / auth.
Status: ✅ selesai · 🔧 sebagian · ⬜ belum · 🚧 terblokir.

> **Sinkron dengan Notion.** Papan kerja harian ada di database Backlog Notion (76 task, lengkap
> dengan Assignee, Prioritas, dan relasi Resource). Berkas ini adalah cerminannya di repo agar
> tetap terbaca tanpa akses Notion — **kalau keduanya berbeda, Notion yang benar.** Perbarui
> berkas ini setiap kali satu epik bergerak, jangan menunggu semuanya selesai.

**Blocker lintas-epik yang sedang berlaku (per 2026-08-13):**
`edge-api` **belum punya lapisan basis data sama sekali** — tak ada pgx di `go.mod`, dan
`config.Store` menyebut `memory|postgres` padahal hanya `memory` yang terimplementasi. Ini
memblokir seluruh Epik 5 kecuali 5.5, dan menahan bagian "muat dari DB" pada task 2.1.
Akarnya: PostgreSQL lokal butuh Docker, yang di Resources Notion masih berstatus *Belum Ada*.

---

## EPIK 1 — Driver & Kontrak Hardware (Dev A) — ✅ TUNTAS
Seluruhnya ada di `services/edge-api/internal/hardware/tcpctl/` (PR #1, #3).
- ✅ 1.1 Paket `internal/hardware/tcpctl`: klien TCP A6/A9 — parser framing Header/Footer.
- ✅ 1.2 Reconnect + exponential backoff + jitter; TCP keep-alive socket.
- ✅ 1.3 Keepalive `PING`/`PINGOK` 1 dtk; tandai OFFLINE setelah 3× gagal; event status device.
- ✅ 1.4 Korelasi perintah↔respons (`OUT1ON`→`OUT1ONOK`) + retry 3× + timeout.
- ✅ 1.5 Debounce input ≥150 ms di Edge; map `INxON/OFF` → `LoopEvent`.
- ✅ 1.6 Parser Wiegand `Wxxxxxx` (W-26/W-34) → `rfid_uid`.
- ✅ 1.7 Implementasi interface Layer-2 (`Barrier/LoopDetector/IndicatorLight/RFIDReader`) di atas tcpctl.
- ✅ 1.8 Ring buffer telemetry TX/RX + audit `≥ warning`.
- ✅ 1.9 Peta pin per gerbang dari `gates.config`; mode palang `hold|pulse`.
- ✅ 1.10 Uji integrasi driver terhadap **simulator TCP device** (`tcpctl/simdev`) — CI-able.
- ✅ 1.11 Interface Layer-2 & simulator perangkat generik (sudah ada dari v2).

## EPIK 2 — Manajer Multi-Gerbang & Keselamatan Edge (Dev A)
- ✅ 2.1 Generalisasi `gatesvc`: N controller + goroutine pemilik per device (PR #4).
  **Sebagian:** memuat daftar `gates` dari DB belum ada — `edge-api` belum punya lapisan DB
  sama sekali dan task 5.1 masih Terblokir. `gatesvc.GateSource` adalah tempat sambungnya;
  sumber saat ini `SpecsFromConfig` (dari `.env`).
- ✅ 2.2 Event WS berlabel `gate_code` + endpoint `/api/v1/sim/gates/:code/...` (PR #4).
- ✅ 2.3 Interlock keselamatan ditegakkan di jalur driver nyata — `tcpctl.Barrier.Close` +
  penjaga perintah di dalam gelang retry (PR #3). Tidak berlaku pada `barrier_mode: pulse`
  (palang turun oleh mekanismenya sendiri; Edge tak punya perintah tutup).
- ✅ 2.4 Timer pengaman Edge: `BARRIER_BLOCKED`, `VEHICLE_STALLED`, `UNAUTHORIZED_PASSAGE`.
  Auto-close tak ditambahkan — FSM sudah menutup lewat timeout `no_show` 45 dtk (lebih ketat dari
  60 dtk). Penyimpangan: `VEHICLE_STALLED` memakai `PatternRedBlink`, sebab peta pin v3 §5.6 tak
  punya kanal kuning.
- ✅ 2.5 State machine masuk & keluar + anti-tailgating (v2, teruji).
- ✅ 2.6 Logika tutup dipicu sensor (LD rising→falling) pada driver nyata — `tcpctl` tersambung ke
  `gatesvc` untuk transport `tcp`.

## EPIK 3 — Ketahanan Edge / Zero-Downtime (Dev A)
- ✅ 3.1 `edge-api` sebagai service systemd/Windows + watchdog — unit `Type=notify` dengan
  `WatchdogSec` (`deploy/systemd/`), NSSM + Scheduled Task untuk Windows (`deploy/windows/`),
  sd_notify di `internal/svcnotify`. Kegagalan fatal kini mematikan proses (dulu menggantung
  hidup tanpa HTTP). Watchdog membuktikan mesin internal, BUKAN kesehatan gerbang (K33).
- ✅ 3.2 Resync `STAT` semua controller saat startup/reconnect (rekonstruksi status) — hanya kanal
  HIGH yang diumumkan; potret tak menimpa kanal yang sudah diketahui.
- ✅ 3.3 Tahan blip koneksi per device — **rekonsiliasi keadaan, bukan antrian perintah**. Perintah
  tetap tak pernah diantre (`ErrNotConnected`); yang disimpan adalah NIAT, lalu ditegaskan ulang
  setelah resync. Rekonsiliasi tutup menuntut bukti positif loop bawah LOW — lebih ketat daripada
  jalur hidup. Lihat K27–K31 di `docs/CATATAN_KEPUTUSAN.md`.
- ✅ 3.4 Healthcheck internal + endpoint kesehatan per gerbang — probe berbatas waktu ke
  goroutine pemilik (gerbang tersendat dilaporkan, bukan menggantungkan healthcheck);
  `GET /api/v1/gates/:code/health`, rollup `gates_status` di `/api/v1/health`, event
  `gate.health.changed` hanya saat status berubah.
- 🔧 3.5 Pemulihan < 15 dtk (NFR-2.3). **Layanan: terpenuhi & terukur** — siklus penuh
  SIGTERM→berhenti→nyala→`READY=1` = **2,01 dtk** terburuk dari 5 putaran (alat ukur:
  `deploy/ukur-pemulihan.sh`). Restart di tengah transaksi diuji dan menemukan bug: palang
  yang ditinggalkan terbuka tak pernah ditutup — kini ditutup saat startup dengan bukti
  positif loop bawah LOW (K36). **Data: BELUM** — `memstore` in-process, restart menghapus
  seluruh kendaraan di dalam lahan. Terblokir task 5.1 (pgx). Lihat K35.
- ⬜ 3.6 Chaos test: cabut LAN controller, matikan Edge, kertas habis, internet putus.

## EPIK 4 — Peripheral Gerbang (Dev A)
- ⬜ 4.1 Adapter Mesin Tiket Otomatis (menunggu H1: protokol/merek) — cetak QR, status kertas, jam.
- ⬜ 4.2 Degradasi kertas habis (casual berhenti, member tetap masuk).
- ⬜ 4.3 Integrasi scanner QR/barcode di POS keluar (USB-HID wedge / serial) (H4).
- ✅ 4.4 Abstraksi `TicketPrinter` + degradasi (v2).

## EPIK 5 — Persistensi & Repository pgx (Dev A) — *tertahan Docker*
- ⬜ 5.1 Repository pgx menggantikan `memstore` (interface identik) — vehicles_log, payments, memberships,
  ocr_logs, audit_logs, outbox, gates/devices.
- ⬜ 5.2 Validasi migrasi goose terhadap PostgreSQL nyata + `grants.sql`.
- ⬜ 5.3 Enforcement `tenant_id` + `site_id` di lapisan repository (§12.14).
- ⬜ 5.4 Seed & fixtures multi-lahan/multi-gerbang.
- ✅ 5.5 Skema DDL + memstore in-memory (v2).

## EPIK 6 — LPR / OCR (Dev A)
- ⬜ 6.1 Klien gRPC nyata ke `lpr-svc` (butuh protoc + stub).
- ⬜ 6.2 Model YOLOv8n + EasyOCR/Tesseract di `lpr-svc` (Fase 2).
- ⬜ 6.3 Trigger snapshot RTSP per gerbang; tulis `ocr_logs` + komparasi foto keluar.
- ✅ 6.4 Normalisasi plat + verdict + abstraksi Recognizer + degradasi UNREAD (v2).

## EPIK 7 — Pembayaran (Dev A)
- 🔧 7.1 Adapter EDC/JTMO fisik (menunggu unit + SDK; simulator sudah ada) (Q1/Q2).
- ✅ 7.2 Tunai, EDC-sim, member, aturan D8 timeout≠gagal (v2).
- ✅ 7.3 QRIS/e-wallet mint + webhook (signature+idempotensi), PG disimulasikan (v2).
- ⬜ 7.4 Rekonsiliasi shift end-to-end + laporan (§6.4) tersambung data nyata.

## EPIK 8 — Sync & Agregasi (Dev A + Dev B)
- ✅ 8.1 Outbox transaksional + sync agent + backoff + HTTP sink (v2, e2e).
- ✅ 8.2 Cloud receiver idempoten per node (v2).
- ⬜ 8.3 mTLS per node (FR-1.4) + Cloudflare Tunnel produksi.
- ⬜ 8.4 Tabel `nodes` + kesehatan Edge per lahan di Pusat.
- ⬜ 8.5 Sync audit_logs jalur terpisah (urutan seq) + verifikasi kontinuitas di Cloud.

## EPIK 9 — Cloud API / Pusat (Dev B)
- ✅ 9.1 Auth login/refresh/logout, RBAC, tenant isolation, /sites, /transactions, /reports, /tariffs,
  /audit/verify, sync/batch, webhook (v2).
- ⬜ 9.2 Endpoint agregasi lintas-lahan (keuangan, volume, okupansi) dari data ter-sync nyata.
- ⬜ 9.3 CRUD memberships pusat + bulk CSV di Cloud (§12.13) + push ke Edge.
- ⬜ 9.4 Manajemen Lahan/Gate/Device (CRUD) dari Pusat.
- ⬜ 9.5 Rate limit + cache in-process (ristretto) (§4.3).

## EPIK 10 — Dashboard Pusat (Tier-3) (Dev B)
- 🔧 10.1 Pisahkan dashboard **Pusat** (ke `cloud-api`) dari **Lokal** (ke `edge-api`).
- ⬜ 10.2 Pemilih Lahan hierarkis + mode "Semua Lahan" agregat.
- ⬜ 10.3 Halaman status semua lahan (kesehatan Edge/gerbang, sync, chain) lintas-lahan.
- ✅ 10.4 12 halaman + login + tema + charts + RBAC menu (v2/v3 UI).
- ⬜ 10.5 Grafik FR-6.2 (okupansi 24 jam, pendapatan 7 hari, donut metode) dari data nyata.
- ⬜ 10.6 Fitur demo "Simulasikan Perusakan" → banner tamper (verifikasi rantai nyata).

## EPIK 11 — Dashboard Lokal & POS (Tier-2) (Dev B)
- ⬜ 11.1 Monitor Lahan **multi-gerbang** (tampilkan N gate masuk/keluar real-time).
- ⬜ 11.2 Hardware Config multi-gerbang: peta port per controller, telemetry hex live, uji perangkat.
- 🔧 11.3 POS kasir keluar tersambung data nyata + scanner + komparasi foto (UI ada, perlu scanner & pgx).
- ⬜ 11.4 Rekonsiliasi Shift UI tersambung backend.
- 🔧 11.5 Field Monitor (kontrol simulator) diperluas ke N gerbang.

## EPIK 12 — Keamanan & Compliance (Dev B)
- ✅ 12.1 JWT/Argon2id, tenant isolation, PAN masked, kredensial via env (v2).
- ⬜ 12.2 mTLS Edge↔Cloud + Cloudflare Access untuk admin Edge.
- ⬜ 12.3 Uji keamanan isolasi tenant lintas-lahan (manipulasi ID) end-to-end.
- ⬜ 12.4 Kebijakan retensi gambar + lifecycle Spaces (§4.4).
- ⬜ 12.5 Audit tembus (append-only enforced di pgx + grants).

## EPIK 13 — DevOps & Rilis (Dev A + Dev B)
- ✅ 13.1 CI (test/lint/build) + Docker publish GHCR (v2).
- ⬜ 13.2 Compose produksi 3-tier + profil Edge vs Cloud.
- ⬜ 13.3 Deploy Cloudflare Tunnel; provisioning DigitalOcean (kredensial klien).
- ⬜ 13.4 Runbook per-tier (Edge on-site, Cloud) + backup/restore teruji.
- ⬜ 13.5 Observability: log terstruktur terpusat, metrik, alert kesehatan lahan.

## EPIK 14 — QA & Serah Terima (Dev A + Dev B)
- ⬜ 14.1 Uji integrasi lintas-tier (device sim → Edge → Cloud → Dashboard).
- ⬜ 14.2 Uji offline 24 jam (nol kehilangan) & chaos (Epik 3.6).
- ⬜ 14.3 UAT lapangan (Fase 1b: firmware nyata, EDC, kamera) — bergantung pihak ketiga.
- ⬜ 14.4 Dokumentasi: manual kasir & admin, panduan troubleshooting perangkat.
- 🔧 14.5 Definition of Done v2 §19 — sebagian terpenuhi di simulator.

---

### Ringkasan lingkup
- **Sudah (✅):** fondasi logika transaksi, sync, auth, dashboard dasar, CI/CD — inti "otak" sistem.
- **Fokus utama menuju enterprise (⬜):** **Epik 1–4** (hardware nyata & multi-gerbang), **Epik 5**
  (pgx/DB nyata), **Epik 10–11** (pemisahan dashboard Pusat/Lokal + multi-gerbang UI), **Epik 13**
  (deploy 3-tier). Ini jalur kritis membawa app dari "teruji di simulator" ke "beroperasi di lahan nyata".
- **Bergantung pihak ketiga:** protokol printer (H1), scanner (H4), EDC/JTMO fisik, kamera, akun PG &
  DigitalOcean, PIC lapangan.
