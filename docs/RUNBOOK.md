# RUNBOOK — Operasi & Deployment

Panduan menjalankan, mendemokan, dan men-deploy sistem. Mengacu PRD Pondasi ([`PRD_PONDASI.md`](PRD_PONDASI.md)).

---

## 1. Prasyarat toolchain

| Alat | Versi | Untuk |
|------|-------|-------|
| Go | 1.22+ | `edge-api`, `cloud-api` |
| Node | 22+ | `dashboard-web` |
| Python | 3.11+ | `lpr-svc` (Fase 2) |
| Docker + Compose | — | PostgreSQL, deploy (NFR-5) |
| goose | terbaru | migrasi DB |

> Catatan mesin dev saat ini: Go dipasang portable di `%LOCALAPPDATA%\Programs\GoPortable`.
> Set `GOROOT` ke sana + tambahkan `$GOROOT/bin` ke PATH sebelum menjalankan `go`.

---

## 2. Menjalankan — Mode Simulator (tanpa DB, untuk demo/latihan)

Filosofi mock (D12): seluruh sistem jalan tanpa PostgreSQL maupun perangkat fisik (P7).

```bash
# Terminal 1 — backend Edge (simulator + in-memory)
cd services/edge-api
NODE_ID=edge-demo EDGE_STORE=memory go run ./cmd/edge-api      # :8080

# Terminal 2 — Cloud (untuk LOGIN dashboard — auth §14)
cd services/cloud-api && go run ./cmd/cloud-api                # :9090

# Terminal 3 — dashboard
cd apps/dashboard-web
npm install     # sekali
npm run dev                                                     # :5173 (proxy /api→:8080, /cloud→:9090)
```

Buka **http://localhost:5173** → **layar Login** (demo: `admin@parkir.local` / `admin12345`,
peran SuperAdmin). Event real-time via `ws://localhost:8080/api/v1/stream`. Menu tampil sesuai
peran (Kasir: POS+Shift; Auditor: read-only; SuperAdmin: semua). Tombol tema terang/gelap di TopBar.

### Skrip demo klien (~1 menit)
1. **Field Monitor** → klik ①→⑥ berurutan (kendaraan kasual masuk: LD1 → tombol → tiket →
   palang buka → LD2 lewat → tutup). State chip & lampu berubah real-time; event mengalir.
2. **Exit POS Kasir** → kendaraan muncul di "Kendaraan di Dalam" → LD3 ↑ → Cari Transaksi
   (`TKT-000001`) → Foto Cocok → CASH (uang cukup) → LD4 ↑/↓.
3. **Dashboard Overview / Catatan Keuangan** → pendapatan & transaksi terisi.
4. **RFID Memberships** → daftar member, coba Bulk CSV (pratinjau → impor).
5. **Field Monitor** → klik "LD2 ↑" tanpa siklus → **Alerts** menampilkan `UNAUTHORIZED_PASSAGE`.

### Sinkronisasi Edge→Cloud (opsional saat demo)
```bash
# Terminal 3 — Cloud
cd services/cloud-api && go run ./cmd/cloud-api                 # :9090
# Jalankan Edge dengan sync aktif:
NODE_ID=edge-demo TENANT_CODE=t-jabar SYNC_CLOUD_ENDPOINT=http://localhost:9090 \
  SYNC_TICK_SECONDS=2 go run ./cmd/edge-api
```
Login Cloud: `admin@parkir.local` / `admin12345`. Transaksi Edge tereplikasi ke Cloud
(`GET /api/v1/transactions` dengan Bearer token).

---

## 3. Menjalankan — Mode PostgreSQL (produksi, perlu Docker)

```bash
cp .env.example .env       # isi kredensial
docker compose up -d edge-db cloud-db
goose -dir db/migrations postgres "$EDGE_DATABASE_URL" up
psql "$EDGE_DATABASE_URL" -f db/grants.sql   # append-only audit_logs
# jalankan dengan EDGE_STORE=postgres (repository pgx — lihat §6 status)
```

---

## 4. Health & observability

- `GET /api/v1/health` (Edge): state gerbang, `sync.pending`, `chain.verified`, `ocr.count`.
- Rantai audit diverifikasi otomatis (cron 03:00) + saat sync; alert `critical` bila rusak.
- CRON 6 job aktif (lihat PRD §8.3).

---

## 5. Troubleshooting

| Gejala | Kemungkinan | Tindakan |
|--------|-------------|----------|
| Dashboard "Terputus" | edge-api mati / port | cek `:8080/api/v1/health`; restart edge-api |
| `sync.pending` naik terus | Cloud down | normal (offline-first); antrean tetap aman, drain saat online |
| Gerbang tak merespons tombol sim | body tanpa `Content-Type: application/json` | pakai header JSON (dashboard sudah benar) |
| `chain.verified=false` | rantai audit rusak | jangan hapus data; eskalasi — Cloud simpan hash pembanding |
| Palang tak menutup | loop bawah HIGH (interlock P4) | benar secara desain; tunggu loop LOW / cek kendaraan |
| Kertas habis | printer PAPER_OUT | casual berhenti (LOCKED_NO_PAPER); **member tetap bisa masuk** (D3) |
| Gerbang `UNRESPONSIVE` di health | controller hang: socket hidup tapi `PING` tak dibalas 3× | driver memutus paksa & menyambung ulang sendiri; bila berulang, cek daya/firmware controller |
| Gerbang `DISCONNECTED` menetap | kabel LAN / IP-port salah / controller mati | cek `gates.endpoint`; driver terus mencoba dengan backoff (maks 15 dtk), tak perlu restart edge-api |
| Perintah palang ditolak `controller sedang terputus` | benar secara desain | perintah **tidak** diantre — kirim ulang setelah gerbang ONLINE (mencegah palang menutup terlambat di atas kendaraan) |
| Loop "berkedip" tapi state machine diam | pantulan kontak < 150 ms | benar secara desain (debounce §6.1); bila kendaraan nyata tak terdeteksi, kalibrasi loop detector di sisi lapangan |
| edge-api gagal start, log "konfigurasi gerbang tidak sah" | `code` gerbang kembar / transport tcp tanpa endpoint | perbaiki daftar gerbang; startup sengaja digagalkan agar tak muncul gerbang berperilaku ganjil |

---

## 6. Status implementasi vs Definition of Done (§19)

**Selesai & teruji (di atas simulator + in-memory):** state machine masuk/keluar, anti-tailgating,
interlock, fare engine, audit chain, pembayaran (tunai/EDC-sim/member; QRIS+webhook logika),
outbox+sync (terbukti e2e), CRON, LPR degradasi+ocr_logs, auth+RBAC, isolasi multi-tenant,
12 halaman dashboard, bulk CSV.

**Ditambahkan v3 (Epik 1 & sebagian Epik 2):** driver controller **A6/A9-TCP** lengkap
(`internal/hardware/tcpctl`) — framing, auto-reconnect backoff+jitter, keepalive PING/PINGOK dengan
deteksi controller bisu, korelasi perintah↔respons + retry, debounce input ≥150 ms, parser Wiegand,
interface Layer-2, peta pin per gerbang, ring buffer telemetry, dan **simulator controller TCP**
(`tcpctl/simdev`) untuk uji integrasi CI. Interlock P4 kini ditegakkan di jalur driver nyata, bukan
hanya simulator. `gatesvc` mengelola **N gerbang** dengan satu goroutine pemilik per gerbang, dan
semua event berlabel `gate_code`.

> **Catatan penting:** driver A6/A9 sudah lengkap tetapi **belum tersambung ke jalur produksi** —
> `gatesvc` masih merangkai simulator untuk setiap gerbang. Menyambungkan `tcpctl.Gate` untuk
> gerbang bertransport `tcp` belum dikerjakan.

**Tertahan blocker (bukan pekerjaan logika):**
- Repository **pgx** + validasi migrasi → butuh Docker/PostgreSQL berjalan. Selama ini belum ada,
  `edge-api` berjalan sepenuhnya in-memory dan daftar gerbang dibaca dari `.env`, bukan tabel `gates`.
- **LPR** transport gRPC nyata + model YOLOv8/EasyOCR → Fase 2 (protoc + model).
- **EDC/JTMO fisik**, **firmware controller**, **kamera** → pihak ketiga (§1.3, Fase 1b).
- **PG nyata (Midtrans/Xendit)**, **DigitalOcean**, **Cloudflare Tunnel** → sumber daya klien.

**Keputusan klien memblokir (§18.3):** Q1 (definisi JTMO), Q4 (anggaran), Q7 (protokol palang),
Q10 (PIC lapangan).
