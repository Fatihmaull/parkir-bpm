# Smart Gate & Parking Management System

Sistem manajemen gerbang & parkir _offline-first_, multi-tenant. Implementasi mengacu pada
**PRD Pondasi v2.0.0** ([`docs/PRD_PONDASI.md`](docs/PRD_PONDASI.md)).

> **Source of truth:** PRD Pondasi. Jika kode dan PRD bertentangan, PRD menang sampai PRD direvisi.

---

## Prinsip Arsitektur (ringkas — lihat PRD §3)

| # | Prinsip |
|---|---------|
| P1 | Gerbang harus tetap hidup tanpa internet. Cloud = tujuan replikasi, bukan dependensi runtime. |
| P2 | Ketersediaan menang atas kesempurnaan data (LPR gagal → masuk dengan plat `UNREAD`). |
| P3 | Perangkat keras tidak dipercaya (timeout, retry terbatas, jalur degradasi). |
| P4 | Keselamatan fisik = interlock keras (palang tak menutup saat loop bawah HIGH). |
| P5 | Uang & audit bersifat _append-only_. |
| P6 | Multi-tenant sejak baris pertama (`tenant_id`, `site_id`). |
| P7 | Semua perangkat punya simulator. |

## Topologi (lihat PRD §4)

```
SITE (Edge Node PC)                          CLOUD (DigitalOcean)
├── edge-api    (Go + Fiber)                 ├── cloud-api      (Go + Fiber)
│   ├ Gate Controller (state machine)        │   ├ Sync Receiver
│   ├ Fare Engine                            │   ├ Aggregation
│   ├ Payment Adapter (EDC/QRIS/e-wallet)    │   ├ Audit Verifier
│   ├ Audit Chain                            │   └ Dashboard API
│   └ Sync Agent ──── Cloudflare Tunnel ────►│
├── edge-db     (PostgreSQL 16 + outbox)     ├── cloud-db       (Managed PostgreSQL)
├── lpr-svc     (Python + gRPC :50051)       ├── dashboard-web  (React + Vite)
└── pos-web     (React + Vite, localhost)
```

## Struktur Repository

```
parkir/
├── docs/                    Dokumen: PRD, catatan desain
├── db/migrations/           Skema PostgreSQL (goose) — PRD §11
├── proto/                   Kontrak protokol perangkat — PRD §5.3
├── services/
│   ├── edge-api/            Backend Edge (Go)   — logika transaksi, state machine, audit
│   ├── cloud-api/           Backend Cloud (Go)  — sync, agregasi, dashboard API
│   └── lpr-svc/             LPR/OCR (Python + gRPC)
├── apps/
│   ├── dashboard-web/       Dashboard pusat (React + Vite)
│   └── pos-web/             POS kasir gerbang keluar (React + Vite)
├── packages/
│   └── types/              Tipe TypeScript bersama (kontrak data)
├── docker-compose.yml       Orkestrasi lokal (NFR-5: restart always)
└── .env.example             Template konfigurasi
```

> **Catatan:** `apps/dashboard-web` & `apps/pos-web` merupakan hasil migrasi dari prototype
> `apps/mvp-demo` (React 19 + TS + Tailwind v4 + Zustand). Strategi migrasi bertahap: PRD §11.3.

## Toolchain

| Komponen | Kebutuhan |
|----------|-----------|
| Go       | 1.22+ (edge-api, cloud-api) — **belum terpasang di mesin ini** |
| Node     | 22+ (web apps, packages) ✅ |
| Python   | 3.11+ (lpr-svc) ✅ |
| Docker   | + Compose (orkestrasi) ✅ |
| goose    | Migrasi DB — `go install github.com/pressly/goose/v3/cmd/goose@latest` |

## Mulai Cepat (dev)

```bash
cp .env.example .env          # isi kredensial
docker compose up -d edge-db  # PostgreSQL lokal
# migrasi:
goose -dir db/migrations postgres "$EDGE_DATABASE_URL" up
# jalankan service (butuh Go terpasang):
cd services/edge-api && go run ./cmd/edge-api
```

## Peran Tim & Timeline

- **Dev A** — Edge, hardware bridge, pembayaran, sync.
- **Dev B** — Cloud, dashboard, POS, auth.
- Timeline 4 minggu, gerbang keluar (_exit criteria_) per minggu: PRD §16.3.
- Definition of Done: PRD §19.

## Open Questions yang memblokir (PRD §18.3)

Q1 (definisi "JTMO"), Q7 (merek palang/controller — protokol §5.3 bisa diubah?), dan Q10 (PIC tim
lapangan) **memblokir jalur kritis sejak hari pertama**. Q4 (anggaran Rp54jt vs Rp59jt) harus dijawab
sebelum kontrak.
