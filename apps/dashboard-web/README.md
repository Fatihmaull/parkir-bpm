# dashboard-web

Dashboard pusat (React + Vite). Hasil migrasi dari prototype `apps/mvp-demo` (React 19 + TS +
Tailwind v4 + Zustand) — PRD §11.3.

## Cara memulai (migrasi bertahap, PRD §11.3)

1. Salin sumber prototype `apps/mvp-demo` ke sini.
2. Ekstrak tipe → gunakan `@parkir/types` (sudah tersedia di `packages/types`).
3. Bangun `api-client` dengan antarmuka identik dengan aksi Zustand saat ini.
4. Ganti isi tiap aksi store: mutasi lokal → panggilan API (satu halaman per commit).
5. Pertahankan feature flag `VITE_USE_MOCK` sampai akhir proyek (PRD D12).

## 12 Halaman (PRD §12) + RBAC (§12.0)

| Halaman | SuperAdmin | Auditor | Kasir |
|---------|:---:|:---:|:---:|
| Dashboard Overview | ✅ | 👁 | ❌ |
| Catatan Keuangan | ✅ | 👁 | ❌ |
| Volume & Jenis Kendaraan | ✅ | 👁 | ❌ |
| Mapping Slot Parkir | ✅ | ❌ | ❌ |
| Konfigurasi Lahan | ✅ | ❌ | ❌ |
| Rekonsiliasi Shift | ✅ | 👁 | ✅ (sendiri) |
| Notifikasi & Alerts | ✅ | 👁 | ❌ |
| Field Monitor | ✅ | ❌ | ❌ |
| Exit POS Cashier | ✅ | ❌ | ✅ → lihat `pos-web` |
| RFID Memberships | ✅ | 👁 | ❌ |
| Hardware Config (COM) | ✅ | ❌ | ❌ |
| Audit Ledger Chain | ✅ | 👁+verify | ❌ |

RBAC di-*enforce* di dua lapis: menu frontend (UX) + middleware backend (keamanan).
