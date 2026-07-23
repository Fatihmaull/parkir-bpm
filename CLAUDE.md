# Panduan Kontributor — parkir

Panduan singkat untuk agen & developer. **Source of truth: [`docs/PRD_PONDASI.md`](docs/PRD_PONDASI.md).**

## Prinsip yang mengikat (PRD §3)

P1 offline-first · P2 ketersediaan > kesempurnaan data · P3 hardware tak dipercaya ·
P4 interlock keselamatan keras · P5 uang & audit append-only · P6 multi-tenant sejak baris pertama ·
P7 semua perangkat punya simulator. **Jika implementasi melanggar salah satu prinsip, implementasi
itu yang salah.**

## Aturan non-negotiable

- Uang = `BIGINT` rupiah utuh, **tidak pernah float** (D6).
- `audit_logs` & `payments` **append-only** — koreksi via entri kompensasi (VOID/ADJUSTMENT), bukan UPDATE.
- Setiap query operasional **wajib** menyertakan `tenant_id` di lapisan repository (P6, §12.14).
- Tarif **versioned** — ubah tarif = baris `tariffs` baru, bukan UPDATE (D5).
- PAN kartu: **hanya masked** — PAN lengkap tak pernah disimpan/di-log (§6.2.3).
- Kredensial PG **hanya di Cloud**, tak pernah di Edge (D7).
- Nol kredensial hardcoded — semua via `.env` (NFR-3).

## Struktur & perintah

| Area | Lokasi | Uji |
|------|--------|-----|
| Skema DB | `db/migrations/` (goose) | `goose -dir db/migrations postgres "$EDGE_DATABASE_URL" up` |
| edge-api (Go) | `services/edge-api/` | `go test ./...` · `golangci-lint run` |
| cloud-api (Go) | `services/cloud-api/` | `go test ./...` |
| lpr-svc (Python) | `services/lpr-svc/` | `pytest` |
| Kontrak perangkat | `proto/device_protocol.md` | — |
| Tipe bersama | `packages/types/` | `tsc --noEmit` |

Perintah umum tersedia di `Makefile` (`make db-up`, `make migrate`, `make test`).

## Definition of Done per task (PRD §19.2)

Kode ter-review · lint bersih (`golangci-lint`, ESLint) · unit test untuk logika bisnis · di-tes
terhadap simulator · tag `FR-x.y` dirujuk di commit · dokumentasi diperbarui bila kontrak berubah ·
**tanpa TODO menggantung di jalur kritis**.

## Peran

Dev A = Edge/hardware/pembayaran/sync. Dev B = Cloud/dashboard/POS/auth. Timeline & exit criteria
per minggu: PRD §16.3.
