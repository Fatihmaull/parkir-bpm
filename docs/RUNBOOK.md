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

# Terminal 2 — dashboard
cd apps/dashboard-web
npm install     # sekali
npm run dev                                                     # :5173 (proxy /api → :8080)
```

Buka **http://localhost:5173**. Event real-time via `ws://localhost:8080/api/v1/stream`.

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

---

## 6. Status implementasi vs Definition of Done (§19)

**Selesai & teruji (di atas simulator + in-memory):** state machine masuk/keluar, anti-tailgating,
interlock, fare engine, audit chain, pembayaran (tunai/EDC-sim/member; QRIS+webhook logika),
outbox+sync (terbukti e2e), CRON, LPR degradasi+ocr_logs, auth+RBAC, isolasi multi-tenant,
12 halaman dashboard, bulk CSV.

**Tertahan blocker (bukan pekerjaan logika):**
- Repository **pgx** + validasi migrasi → butuh Docker/PostgreSQL berjalan.
- **LPR** transport gRPC nyata + model YOLOv8/EasyOCR → Fase 2 (protoc + model).
- **EDC/JTMO fisik**, **firmware controller**, **kamera** → pihak ketiga (§1.3, Fase 1b).
- **PG nyata (Midtrans/Xendit)**, **DigitalOcean**, **Cloudflare Tunnel** → sumber daya klien.

**Keputusan klien memblokir (§18.3):** Q1 (definisi JTMO), Q4 (anggaran), Q7 (protokol palang),
Q10 (PIC lapangan).
