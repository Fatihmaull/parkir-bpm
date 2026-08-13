# Panduan Kontributor — parkir

Panduan singkat untuk agen & developer.

**Source of truth berlapis:** [`docs/PRD_PONDASI.md`](docs/PRD_PONDASI.md) (v2) untuk detail
transaksional yang tak berubah — state machine, fare engine, rantai audit, tarif versioned.
[`docs/PRD_v3_ENTERPRISE.md`](docs/PRD_v3_ENTERPRISE.md) **menang** untuk arsitektur baru: topologi
3-tier, kontrak hardware A6/A9-TCP, multi-gerbang, zero-downtime.

## Prinsip yang mengikat (PRD §3)

P1 offline-first · P2 ketersediaan > kesempurnaan data · P3 hardware tak dipercaya ·
P4 interlock keselamatan keras · P5 uang & audit append-only · P6 multi-tenant sejak baris pertama ·
P7 semua perangkat punya simulator · P8 zero-downtime operasional per lahan ·
P9 controller tidak dipercaya untuk keselamatan. **Jika implementasi melanggar salah satu prinsip,
implementasi itu yang salah.**

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
| Driver controller A6/A9 | `services/edge-api/internal/hardware/tcpctl/` | `go test ./internal/hardware/tcpctl/...` |
| Simulator controller TCP | `.../tcpctl/simdev/` — dipakai uji integrasi & dev lokal | — |
| Manajer N gerbang | `services/edge-api/internal/gatesvc/` | `go test ./internal/gatesvc/` |
| cloud-api (Go) | `services/cloud-api/` | `go test ./...` |
| lpr-svc (Python) | `services/lpr-svc/` | `pytest` |
| Kontrak perangkat (sumber kebenaran) | `docs/PRD_v3_ENTERPRISE.md` §5–§6 — protokol A6/A9-TCP | — |
| Kontrak perangkat (dok. lapangan) | `proto/device_protocol.md` — **DRAFT**, audiens tim instalasi, jangan dikirim keluar | — |
| Papan kerja | Notion Backlog (utama) · `docs/TASKS.md` (cermin di repo) | — |
| Tipe bersama | `packages/types/` | `tsc --noEmit` |

Perintah umum tersedia di `Makefile` (`make db-up`, `make migrate`, `make test`).

## Kondisi implementasi saat ini (per 2026-08-13)

Baca ini sebelum merencanakan pekerjaan — beberapa hal yang tampak "tinggal dipakai" belum ada.

- **Tidak ada lapisan basis data di `edge-api`.** Tak ada pgx di `go.mod`; `config.Store` menyebut
  `memory|postgres` tapi hanya `memory` (`internal/memstore`) yang jalan. Ini memblokir Epik 5
  (kecuali 5.5) dan bagian "muat gerbang dari DB" pada task 2.1.
- **Driver A6/A9 (`tcpctl`) sudah tersambung ke jalur produksi** sejak task 2.6 — `gatesvc`
  merangkai `tcpctl.Gate` untuk gerbang bertransport `tcp`. Yang MASIH tersimulasi: printer di
  gerbang masuk (task 4.1 terblokir H1). Daftarnya terbuka di `gatesvc.Runner.Disimulasikan()`
  dan `/api/v1/gates` — jangan mengira seluruh gerbang sudah sungguhan.
- **Printer tiket belum punya adapter konkret** — terblokir H1 (merek/protokol belum dikonfirmasi
  klien). Gerbang masuk nyata belum bisa lengkap tanpa ini.
- `go test -race` **tidak bisa jalan di mesin dev Windows** (butuh cgo/gcc). CI Linux adalah
  gerbang race — jangan mengklaim bebas race berdasarkan uji lokal saja.

## Invarian yang mudah dilanggar tanpa sengaja

Ditemukan mahal, jangan dibongkar tanpa alasan kuat:

- **Perintah ke controller tak pernah diantre saat terputus** (`tcpctl.ErrNotConnected`).
  `OUT1OFF` yang tertahan lalu terkirim beberapa detik kemudian bisa menutup palang di atas
  kendaraan lain. Antrian sadar-konteks adalah task 3.3, bukan ditambahkan diam-diam.
- **Interlock diperiksa ulang di setiap percobaan retry**, bukan sekali di awal
  (`tcpctl.WithCommandGuard`). Satu `Exec` merentang ratusan milidetik dan loop bawah bisa
  berubah HIGH di tengahnya.
- **Debounce input berbasis stabilitas, bukan tepi pertama.** Palang menutup pada tepi *turun*
  loop bawah; satu pantulan LOW palsu yang lolos bisa menutup palang di atas kendaraan.
- **Resync STAT hanya mengumumkan kanal HIGH** (`tcpctl.Device.tanamkanStat`, task 3.2). LOW
  adalah keadaan istirahat: saat lahan sepi keempat kanal LOW, jadi mengumumkannya berarti
  memancarkan empat tepi *turun* palsu di tiap reconnect — perintah tutup untuk kendaraan yang
  tak ada. Jangan "melengkapi" resync dengan mengumumkan LOW.
- **Potret STAT tak pernah menimpa kanal yang sudah diketahui** (`Debouncer.Seed`). Balasan STAT
  bisa tiba setelah event `INxON/INxOFF` yang lebih baru; menimpanya memundurkan status ke
  masa lalu.
- **Setiap gerbang dimiliki satu goroutine** (`gatesvc.Runner`). Jangan menambahkan mutex global
  atau menyentuh state machine dari luar inbox-nya.
- **Setiap event membawa `gate_code`.** Jangan menambah event tanpa label itu — lahan bisa punya
  lebih dari satu gerbang masuk.

## Definition of Done per task (PRD §19.2)

Kode ter-review · lint bersih (`golangci-lint`, ESLint) · unit test untuk logika bisnis · di-tes
terhadap simulator · tag `FR-x.y` dirujuk di commit · dokumentasi diperbarui bila kontrak berubah ·
**tanpa TODO menggantung di jalur kritis**.

## Peran

Dev A = Edge/hardware/pembayaran/sync. Dev B = Cloud/dashboard/POS/auth. Timeline & exit criteria
per minggu: PRD §16.3.
