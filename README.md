# Smart Gate & Parking Management System

[![CI](https://github.com/Fatihmaull/parkir-bpm/actions/workflows/ci.yml/badge.svg)](https://github.com/Fatihmaull/parkir-bpm/actions/workflows/ci.yml)

Sistem manajemen gerbang & parkir _offline-first_, multi-tenant.

> **Source of truth berlapis.** [`docs/PRD_PONDASI.md`](docs/PRD_PONDASI.md) (v2) memegang detail
> transaksional yang tidak berubah: state machine gerbang, fare engine, rantai audit, tarif
> versioned. [`docs/PRD_v3_ENTERPRISE.md`](docs/PRD_v3_ENTERPRISE.md) **menang** untuk arsitektur
> baru: topologi 3-tier, kontrak hardware A6/A9-TCP, manajer multi-gerbang, zero-downtime.
> Ringkasan perbedaannya: [`docs/CHANGES_v2_to_v3.md`](docs/CHANGES_v2_to_v3.md).
> Jika kode dan PRD bertentangan, PRD menang sampai PRD direvisi.

**Progres pekerjaan:** papan utama di Notion (database Backlog); cerminannya di repo ada di
[`docs/TASKS.md`](docs/TASKS.md).

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
| P8 | Zero-downtime operasional per lahan (v3). |
| P9 | Controller tidak dipercaya untuk keselamatan — interlock & timer ditegakkan di Edge (v3). |

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
│   │   └── internal/
│   │       ├── hardware/tcpctl/   Driver controller A6/A9-TCP (+ simdev: simulator TCP)
│   │       └── gatesvc/           Manajer N gerbang, satu goroutine pemilik per gerbang
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
| Go       | 1.22+ (edge-api, cloud-api) |
| Node     | 22+ (web apps, packages) ✅ |
| Python   | 3.11+ (lpr-svc) ✅ |
| Docker   | + Compose (orkestrasi) ✅ |
| goose    | Migrasi DB — `go install github.com/pressly/goose/v3/cmd/goose@latest` |

## Mulai Cepat (dev)

**Mode simulator (tanpa DB, tanpa hardware — P7/D12).** `edge-api` dapat langsung dijalankan di atas
simulator perangkat + penyimpanan in-memory:

```bash
cd services/edge-api
NODE_ID=edge-dev EDGE_DATABASE_URL=postgres://x/y go run ./cmd/edge-api
```

Lalu gerakkan gerbang lewat endpoint Field Monitor (§12.8) — contoh siklus masuk:

```bash
curl -XPOST localhost:8080/api/v1/sim/entry/loop -d '{"loop":"pre","high":true}'   # LD1
curl -XPOST localhost:8080/api/v1/sim/entry/button                                  # ambil tiket
curl -XPOST localhost:8080/api/v1/sim/entry/ticket-taken                            # tiket diambil → palang buka
curl -XPOST localhost:8080/api/v1/sim/entry/loop -d '{"loop":"post","high":true}'   # LD2 naik
curl -XPOST localhost:8080/api/v1/sim/entry/loop -d '{"loop":"post","high":false}'  # LD2 turun → tutup
curl localhost:8080/api/v1/health                                                   # lihat state + rantai audit
```

Endpoint di atas menyentuh gerbang **pertama** tiap jenis. Sejak multi-gerbang (task 2.1/2.2) ada
jalur yang beralamat `gate_code` — pakai ini untuk kode baru:

```bash
curl localhost:8080/api/v1/gates                                   # daftar gerbang + state
curl -XPOST localhost:8080/api/v1/sim/gates/GATE-IN-01/loop -d '{"loop":"pre","high":true}'
curl localhost:8080/api/v1/gates/GATE-IN-01/state
```

Event real-time via WebSocket: `ws://localhost:8080/api/v1/stream`. Setiap event membawa
`gate_code` + `gate_kind`, sehingga lahan dengan lebih dari satu gerbang masuk tetap terbedakan.

**Mode dengan PostgreSQL (produksi):**

```bash
cp .env.example .env          # isi kredensial
docker compose up -d edge-db  # PostgreSQL lokal
goose -dir db/migrations postgres "$EDGE_DATABASE_URL" up
```

> Repository pgx menggantikan memstore lewat interface yang identik (lihat `internal/memstore`) —
> logika gerbang tidak berubah.

## Peran Tim & Timeline

- **Dev A** — Edge, hardware bridge, pembayaran, sync.
- **Dev B** — Cloud, dashboard, POS, auth.
- Timeline 4 minggu, gerbang keluar (_exit criteria_) per minggu: PRD §16.3.
- Definition of Done: PRD §19.

## Open Questions yang memblokir (PRD §18.3)

Q1 (definisi "JTMO"), Q7 (merek palang/controller — protokol §5.3 bisa diubah?), dan Q10 (PIC tim
lapangan) **memblokir jalur kritis sejak hari pertama**. Q4 (anggaran Rp54jt vs Rp59jt) harus dijawab
sebelum kontrak.
