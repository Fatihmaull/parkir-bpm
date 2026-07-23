# pos-web

Antarmuka kasir gerbang keluar (React + Vite), berjalan di `localhost` Edge Node, terhubung ke
`edge-api` via WebSocket. PRD §4.2 / §12.9.

Target kecepatan: **< 15 detik per kendaraan**; update UI **< 500 ms** setelah settlement (NFR-1.3).

## Fitur inti (PRD §12.9)

- Panel kendaraan terdeteksi (dari LD3 + LPR).
- **Komparasi foto berdampingan** (FR-3.1): foto masuk vs snapshot langsung, tombol besar
  "Cocok" / "Tidak Cocok".
- Rincian tarif (waktu masuk, durasi, jenis, tarif, total).
- Pemilih metode bayar — metode offline-incapable (QRIS/EWALLET) **otomatis disabled saat offline**
  (PRD §6.1) dengan tooltip "Tidak tersedia — mode offline".
- Aksi khusus: tiket hilang, input tiket manual, batalkan transaksi (ter-audit).
- Shortcut keyboard untuk seluruh aksi utama — kasir tidak bergantung pada mouse.

## Catatan

Dibangun dari komponen prototype `apps/mvp-demo`. Gunakan `@parkir/types` untuk kontrak data.
Koreksi manual plat di POS **otomatis** menulis `corrected_plate` ke `ocr_logs` (PRD §7.2).
