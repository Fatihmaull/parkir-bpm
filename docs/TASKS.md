# Tasking — Menuju Enterprise-Ready (2 Programmer)

Dipecah per **epik**, tanpa timeline. Tujuan: melihat total lingkup kerja membawa app ke siap-produksi.
**Dev A** = Edge / hardware / pembayaran / sync. **Dev B** = Cloud / dashboard / POS / auth.
Status: ✅ selesai · 🔧 sebagian · ⬜ belum · 🚧 terblokir.

> **Sinkron dengan Notion.** Papan kerja harian ada di database Backlog Notion (76 task, lengkap
> dengan Assignee, Prioritas, dan relasi Resource). Berkas ini adalah cerminannya di repo agar
> tetap terbaca tanpa akses Notion — **kalau keduanya berbeda, Notion yang benar.** Perbarui
> berkas ini setiap kali satu epik bergerak, jangan menunggu semuanya selesai.

**Blocker lintas-epik SELESAI (per 2026-08-18):** ~~`edge-api` belum punya lapisan basis
data~~ — `internal/pgstore` ada, Epik 5 tuntas, dan task 2.1 (muat gerbang dari DB) ikut
selesai menyusul. Ternyata TAK butuh Docker (lihat CATATAN_KEPUTUSAN.md K44). Blocker
lintas-epik yang MASIH berlaku sekarang seluruhnya bergantung pihak ketiga — lihat §9
`CATATAN_KEPUTUSAN.md` (H1–H7): protokol printer, scanner, EDC/JTMO fisik, kamera.

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
  **Muat dari DB — SELESAI menyusul Epik 5.** `internal/pgstore` mengimplementasikan
  `gatesvc.GateSource` (`LoadGates`, di `gates.go`) — mode `EDGE_STORE=postgres` memuat
  gerbang dari tabel `gates` (hanya `status='active'`, terurut `code`), mode memory tetap
  `SpecsFromConfig` (dari `.env`). Diuji ujung-ke-ujung: biner `edge-api` sungguhan +
  `TENANT_CODE=dev_jabar SITE_CODE=mall_jabar` (3 gerbang di `db/seed/dev_seed.sql`) →
  `/api/v1/gates` mengembalikan **3 gerbang** (2 masuk + 1 keluar) — mustahil dari `.env`,
  yang cuma pernah menghasilkan tepat 2. Site tanpa gerbang aktif gagal keras saat startup
  (bukan diam-diam jatuh ke `DefaultSpecs`) — "lupa seed" harus kelihatan sebagai kesalahan
  konfigurasi, bukan tersamar jadi "lahan demo sengaja". Lihat K47.
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

## EPIK 3 — Ketahanan Edge / Zero-Downtime (Dev A) — ✅ TUNTAS (termasuk pemulihan DATA, 3.5)
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
- ✅ 3.5 Pemulihan < 15 dtk (NFR-2.3). **Layanan: terpenuhi & terukur** — siklus penuh
  SIGTERM→berhenti→nyala→`READY=1` = **2,01 dtk** terburuk dari 5 putaran (alat ukur:
  `deploy/ukur-pemulihan.sh`). Restart di tengah transaksi diuji dan menemukan bug: palang
  yang ditinggalkan terbuka tak pernah ditutup — kini ditutup saat startup dengan bukti
  positif loop bawah LOW (K36). **Data: SELESAI** (menyusul Epik 5) — dengan
  `EDGE_STORE=postgres`, kendaraan yang masuk SEBELUM restart tetap dikenali & bisa keluar
  normal SESUDAHNYA, dibuktikan `TestVehicleDataSurvivesRestart` (`internal/pgstore`,
  `go test -tags=integration`, terhadap Postgres sungguhan — bukan memstore, yang memang
  masih kehilangan semuanya kalau dipakai). Lihat K35 (ditandai selesai) & K39–K46.
- ✅ 3.6 Chaos test lahan (`gatesvc/chaos_test.go`): cabut LAN satu gerbang (lahan tetap
  melayani, P8), kertas habis (casual berhenti, member tetap masuk, D3), internet putus
  (gerbang tak tersentuh, outbox menumpuk, P1), + semua rusak sekaligus. "Edge mati" diuji
  di lapisan driver (task 3.5). **Menemukan bug: `LOCKED_NO_PAPER` tak punya jalan keluar
  sama sekali** — D3 tak berlaku di lapangan, dan gerbang tetap mati walau kertas diisi
  ulang. Diperbaiki + 3 uji unit. Lihat K38.

## EPIK 4 — Peripheral Gerbang (Dev A)
- ⬜ 4.1 Adapter Mesin Tiket Otomatis (menunggu H1: protokol/merek) — cetak QR, status kertas, jam.
- ✅ 4.2 Degradasi kertas habis (casual berhenti, member tetap masuk). Sudah terimplementasi &
  teruji sejak Epik 4/hardware: `internal/gate/entry.go` mengunci jalur kasual saat printer
  `PAPER_OUT`, member tetap boleh masuk (tanpa tiket cetak), dan terbuka lagi begitu kertas
  diisi ulang — `TestPaperOutLocksCasual`, `TestPaperOutMemberTetapMasuk`,
  `TestPaperOutPulihSetelahKertasDiisi`, `TestPaperOutKendaraanPergiMembukaKunci`
  (`internal/gate/entry_test.go`), plus `TestChaosKertasHabis` (`internal/gatesvc/chaos_test.go`)
  dan `TestPrinterPaperOutBlocksPrint` (`internal/hardware/sim/sim_test.go`). Diverifikasi ulang
  lewat inspeksi kode+test (bukan diasumsikan) sebelum ditandai selesai di sini.
- ⬜ 4.3 Integrasi scanner QR/barcode di POS keluar (USB-HID wedge / serial) (H4).
- ✅ 4.4 Abstraksi `TicketPrinter` + degradasi (v2).

## EPIK 5 — Persistensi & Repository pgx (Dev A) — ✅ TUNTAS
Ternyata TAK butuh Docker sama sekali — sandbox pengerjaan ini sudah punya paket
`postgresql-16` terpasang (server, bukan cuma client), jadi seluruh task di bawah diuji
terhadap Postgres SUNGGUHAN secara lokal, bukan cuma `go vet`. Lihat CATATAN_KEPUTUSAN.md K41.
- ✅ 5.1 Repository pgx (`internal/pgstore`) menggantikan `memstore` lewat kontrak identik
  (`gatesvc.Store`, interface baru yang disatukan) — vehicles_log, payments, memberships,
  tariffs, ocr_logs, audit_logs (rantai hash persisten lintas restart), outbox (transaksional,
  tabel `sync_outbox`), ticket_counters (penomoran tiket atomik per site, migrasi baru 00006).
  Diuji ujung-ke-ujung: biner `edge-api` sungguhan + `EDGE_STORE=postgres` + gerbang simulator,
  kendaraan lewat GATE-IN-01 sampai `vehicles_log` COMMITTED, `audit_logs` bertaut hash benar,
  `sync_outbox` terisi transaksional. 2 bug nyata ditemukan & diperbaiki lewat uji integrasi
  (bukan dugaan): (1) `flags` nil di `Complete` membentur NOT NULL constraint — memstore tak
  pernah menyentuh ini karena map Go menerima nil slice tanpa keluhan; (2) hash rantai audit
  dihitung dari `created_at` presisi nanodetik tapi Postgres `timestamptz` hanya presisi
  mikrodetik → `VerifyChain` SELALU melaporkan rantai rusak pada baca-ulang pertama walau tak
  ada yang dimanipulasi, sampai `created_at` dipotong ke mikrodetik SEBELUM dihash.
- ✅ 5.2 Migrasi goose (termasuk 00006 baru) + `db/grants.sql` divalidasi terhadap Postgres 16
  sungguhan — `REVOKE UPDATE/DELETE/TRUNCATE` teruji langsung menolak `app_user` (privilege,
  bukan cuma trigger). CI (`ci.yml`) sekarang menjalankan migrasi + `go test -tags=integration`
  terhadap `postgres:16` sebagai service container GitHub Actions per run — efemeral, tanpa
  Docker di mesin developer mana pun.
- ✅ 5.3 Enforcement `tenant_id`/`site_id` (§12.14) — diikat SEKALI saat `pgstore.New` (dari
  `TENANT_CODE`/`SITE_CODE`, di-resolve ke UUID), dipakai di SETIAP query yang ditulis di
  repository, bukan diterima per-panggilan. Realitas fisik "Edge = satu proses per lahan"
  membuat pengikatan di konstruksi ini mustahil dilewatkan, dibanding mempercayai tiap
  pemanggil mengirim tenant_id benar tiap kali.
- ✅ 5.4 Seed dev multi-lahan/multi-gerbang — `db/seed/dev_seed.sql` (`make seed`): 2 tenant
  (satu dengan 2 site, untuk uji isolasi antar-site DALAM satu tenant — kasus paling gampang
  bocor), site pertama dapat 2 gerbang masuk + 1 keluar. Idempoten (`ON CONFLICT DO NOTHING`),
  diuji dijalankan dua kali berturut-turut.
- ✅ 5.5 Skema DDL + memstore in-memory (v2).

## EPIK 6 — LPR / OCR (Dev A)
- ✅ 6.1 Klien gRPC nyata ke `lpr-svc`. Stub Go di-generate dari `proto/lpr.proto`
  (`protoc`+`protoc-gen-go`+`protoc-gen-go-grpc` → `internal/lpr/lprpb`, DI-COMMIT — beda
  dari sisi Python) dan diimpor `internal/lpr/grpc.go` (`lpr.GRPC`, memenuhi `Recognizer`).
  Sisi Python: `lpr_svc/server.py` sebelumnya cuma print pesan (`TODO Minggu 2`) — sekarang
  benar-benar `grpc.server` yang bind & serve, stub-nya di-generate `gen_proto.sh` (SENGAJA
  tak di-commit, `.gitignore`) supaya tak basi terhadap proto. `main.go`: mode postgres
  mencoba `lpr.NewGRPC(cfg.LPRAddr)`, jatuh ke `lpr.Degraded` (UNREAD) kalau gagal — P2, LPR
  tak pernah jadi gerbang keputusan atau dependensi keras startup.
  "Kecerdasan" di baliknya (YOLOv8n/EasyOCR) TETAP placeholder — itu task 6.2 terpisah;
  6.1 murni transport gRPC-nya, dan itu yang dibuktikan nyata: proses Python sungguhan
  dijalankan, klien Go memanggilnya lewat gRPC beneran (bukan mock), responsnya (termasuk
  `engine_version` dari proses server) mengalir sampai `ocr_logs` di Postgres lalu terbaca
  balik lewat `/api/v1/ocr-logs`. Diuji: `go test -tags=integration ./internal/lpr/...`
  (`TestGRPCRecognizeAgainstRealServer`) + `pytest` Python (`test_server.py`, server
  in-process di port efemeral) + ujung-ke-ujung biner+proses nyata. CI: job `lpr-svc`
  sekarang `pip install -r requirements.txt` (dulu cuma `pytest`) + `./gen_proto.sh`
  sebelum `pytest`, supaya server gRPC beneran ikut teruji tiap push, bukan cuma
  `normalize.py`.
- ⬜ 6.2 Model YOLOv8n + EasyOCR/Tesseract di `lpr-svc` (Fase 2).
- ⬜ 6.3 Trigger snapshot RTSP per gerbang; tulis `ocr_logs` + komparasi foto keluar.
- ✅ 6.4 Normalisasi plat + verdict + abstraksi Recognizer + degradasi UNREAD (v2).

## EPIK 7 — Pembayaran (Dev A)
- 🔧 7.1 Adapter EDC/JTMO fisik (menunggu unit + SDK; simulator sudah ada) (Q1/Q2).
- ✅ 7.2 Tunai, EDC-sim, member, aturan D8 timeout≠gagal (v2).
- ✅ 7.3 QRIS/e-wallet mint + webhook (signature+idempotensi), PG disimulasikan (v2).
- ✅ 7.4 Rekonsiliasi shift end-to-end + laporan (§6.4) tersambung data nyata. Skema `shifts`
  + `payments.shift_id` sudah ada sejak migrasi 00004 (belum dipakai) — logika buka/tutup/
  hitung ditambahkan di `internal/pgstore/shifts.go` + `internal/memstore` (kontrak identik
  lewat `gatesvc.Store`), plus `GET/POST /api/v1/shifts`, `POST /open`, `POST /{id}/close`
  (`cmd/edge-api/shifts.go`). `payments.Begin` menaut ke shift terbuka otomatis lewat
  subquery. Satu shift aktif per site ditegakkan unique index parsial DB (migrasi 00007),
  bukan cek app-level (K48). Selisih ≠ 0 wajib note; di atas `sites.cash_variance_threshold`
  → audit `critical`, di bawahnya → `warning` (pgstore saja — memstore tak audit, K48).
  Diuji: unit test memstore (`go test ./...`) + integrasi pgstore
  (`TestShiftReconciliation`, `go test -tags=integration`) + ujung-ke-ujung lewat biner
  sungguhan (buka shift → HTTP 409 saat buka kedua kalinya → transaksi lewat gerbang
  simulator → tutup shift → laporan). Ketemu bug uji nyata di luar fitur ini sendiri: lihat
  K49 (fixture ID test tak benar-benar unik).

## EPIK 8 — Sync & Agregasi (Dev A + Dev B)
- ✅ 8.1 Outbox transaksional + sync agent + backoff + HTTP sink (v2, e2e).
- ✅ 8.2 Cloud receiver idempoten per node (v2).
- ⬜ 8.3 mTLS per node (FR-1.4) + Cloudflare Tunnel produksi.
- ⬜ 8.4 Tabel `nodes` + kesehatan Edge per lahan di Pusat.
- ✅ 8.5 Sync audit_logs jalur terpisah (urutan seq) + verifikasi kontinuitas di Cloud.
  Jalur TERPISAH dari 8.1 (bukan dipakaikan `sync_outbox` yang sama) — tabel `audit_sync_outbox`
  sendiri (migrasi 00008), interface `outbox.AuditStore` sendiri, agen sync sendiri
  (`internal/auditsync`), endpoint sendiri `POST /internal/v1/sync/audit-batch`. Alasan: satu
  entri audit yang hilang merusak verifikasi SEMUA entri sesudahnya (beda dari vehicles_log,
  yang hilangnya satu baris tak memengaruhi baris lain) — lihat K51.
  `pgstore.Record` menulis `audit_logs` DAN `audit_sync_outbox` dalam SATU transaksi
  (`internal/pgstore/audit.go`); `memstore` punya padanan in-memory
  (`internal/memstore/auditoutbox.go`). `outbox.AuditPG.MarkAuditFailed` TAK PERNAH memindahkan
  status ke FAILED permanen — beda sengaja dari outbox biasa (batas 5 percobaan) — retry
  selamanya, karena celah rantai audit tak punya jalan rekonsiliasi manual sebaik data bisnis.
  Cloud (`services/cloud-api/internal/store/audit.go`) memverifikasi ULANG setiap entri secara
  kriptografis sebelum diterima — kontinuitas seq, sambungan `previous_hash`, dan `current_hash`
  dihitung ulang — bukan sekadar disimpan mentah; formula hash sengaja diduplikasi dari
  `internal/audit` (modul Go terpisah, batas layanan), didokumentasikan di komentar berkas.
  Endpoint baru: `GET /api/v1/audit` (ringkasan kontinuitas per node, atau `?node_id=` untuk
  entri penuh) & `POST /api/v1/audit/verify` (re-hash penuh dari genesis, on-demand §9.4),
  menggantikan stub lama yang selalu `verified:true` tanpa memeriksa apa pun.
  Diuji: unit `internal/memstore` + `internal/auditsync` (drain/backoff/retry-selamanya/HTTP
  sink) + `internal/store` cloud-api (rantai kontinu diterima, celah ditolak TANPA menggerakkan
  checkpoint, hash dimanipulasi ditolak, retry identik idempoten, retry dengan isi beda ditolak,
  isolasi tenant) + integrasi pgstore terhadap Postgres sungguhan
  (`TestAuditOutboxTransactionalWithAuditLog`, `go test -tags=integration`) + ujung-ke-ujung
  lewat kedua biner sungguhan (edge-api mode memory → cloud-api): batch masuk, `/api/v1/audit`
  & `/api/v1/audit/verify` melapor benar, dan percobaan mengirim seq yang melompat (celah)
  ditolak tanpa memajukan checkpoint — diverifikasi langsung lewat HTTP live, bukan cuma `go test`.

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
