# Catatan Keputusan

Rekaman keputusan desain & implementasi proyek parkir, dari inisialisasi monorepo sampai
pekerjaan berjalan. Tujuannya satu: **supaya keputusan yang sudah dibayar mahal tidak dibongkar
ulang tanpa sengaja.**

Tiap entri menyebutkan *harga* keputusannya — apa yang kita relakan. Keputusan tanpa harga
biasanya belum dipikirkan tuntas, dan yang paling sering dibongkar orang adalah keputusan yang
harganya tampak seperti bug.

**Cara pakai:**

- Mau mengubah perilaku yang terasa aneh? Cari dulu di sini. Kalau ada entrinya, keanehan itu
  disengaja — bantah alasannya, jangan cuma perbaiki gejalanya.
- Membuat keputusan baru yang mengikat? Tambahkan entri, jangan hanya tulis di commit message.
- Dokumen ini **bukan** sumber kebenaran spesifikasi. Itu tetap
  [`PRD_PONDASI.md`](PRD_PONDASI.md) (v2, hal transaksional) dan
  [`PRD_v3_ENTERPRISE.md`](PRD_v3_ENTERPRISE.md) (v3, menang untuk arsitektur). Di sini yang
  dicatat adalah **pilihan** dan **alasannya**.

Penomoran: `D*` dari PRD Lampiran A · `V*` perubahan v2→v3 · `K*` keputusan saat implementasi.

---

## 1. Prinsip yang mengikat (PRD §3)

Sembilan prinsip ini mengalahkan semua yang di bawahnya. **Jika implementasi melanggar salah satu
prinsip, implementasi itu yang salah** — bukan prinsipnya.

| | Prinsip |
|---|---|
| P1 | Offline-first |
| P2 | Ketersediaan > kesempurnaan data |
| P3 | Hardware tak dipercaya |
| P4 | Interlock keselamatan keras |
| P5 | Uang & audit append-only |
| P6 | Multi-tenant sejak baris pertama |
| P7 | Semua perangkat punya simulator |
| P8 | Zero-downtime operasional per lahan |
| P9 | Controller tidak dipercaya untuk keselamatan |

Hampir setiap keputusan di bawah adalah turunan dari salah satu prinsip ini. Kalau sebuah entri
terasa berlebihan, periksa prinsip yang dirujuknya dulu.

---

## 2. Keputusan produk & arsitektur (D1–D12)

Sumber: [`PRD_PONDASI.md`](PRD_PONDASI.md) Lampiran A. Diulang di sini supaya satu dokumen cukup.

| # | Keputusan | Alasan |
|---|---|---|
| D1 | Rantai audit per-node, bukan global | Rantai global mustahil dikoordinasikan saat offline |
| D2 | LPR bukan gerbang keputusan | False-positive OCR lebih merusak daripada plat tak terbaca |
| D3 | Member tetap bisa masuk saat kertas habis | Memutus akses penghuni karena printer adalah kegagalan tak perlu |
| D4 | Transactional outbox, bukan message queue | Menjamin atomisitas dengan data bisnis; nol infrastruktur tambahan |
| D5 | Tarif versioned, bukan di-`UPDATE` | Transaksi lama harus dapat direkalkulasi dengan tarif saat itu |
| D6 | Uang sebagai `BIGINT` rupiah, bukan float | Pembulatan float pada uang adalah bug yang menunggu terjadi |
| D7 | Kredensial PG hanya di Cloud | Edge Node di lapangan, lebih rentan secara fisik |
| D8 | Timeout EDC ≠ gagal, harus query batch | Double-charge jauh lebih mahal daripada palang telat 5 detik |
| D9 | Simulator untuk seluruh perangkat | Jalur kritis, timeline 4 minggu, hardware belum pasti |
| D10 | `tenant_id` sejak baris pertama | Menambahkannya belakangan = migrasi data yang menyakitkan |
| D11 | Interlock keselamatan redundan (PC + firmware) | Keselamatan fisik tak boleh bergantung satu titik |
| D12 | Feature flag mock dipertahankan | Kemampuan demo tanpa backend terlalu berharga untuk dibuang |

> **Catatan atas D11 — sudah tak berlaku sepenuhnya.** v3 menemukan controller A6/A9 tidak
> menyediakan interlock di firmware sama sekali (lihat V4). Redundansi yang diandaikan D11 tidak
> ada di lapangan; seluruh beban keselamatan jatuh ke Edge. Ini bukan pilihan kita — ini temuan
> tentang perangkat yang sudah dibeli klien.

---

## 3. Perubahan arsitektur v2 → v3 (V1–V7)

Sumber: [`CHANGES_v2_to_v3.md`](CHANGES_v2_to_v3.md) §B. Dipicu diagram klien + spesifikasi
hardware nyata yang baru diketahui setelah v2 ditulis.

| # | v2 | v3 | Pemicu |
|---|---|---|---|
| V1 | 1 lahan = 1 gerbang masuk + 1 keluar | **1 Edge per lahan mengelola N gerbang** | Diagram klien |
| V2 | Protokol buatan sendiri (STX/CRC16/stuffing) | **Protokol vendor A6/A9-TCP** (Header/Footer, ASCII, tanpa CRC) | Hardware nyata sudah punya standar |
| V3 | Transport RS232 | **TCP/IP LAN**; device = TCP *server*, Edge = client | Spec vendor |
| V4 | Interlock/debounce/auto-close wajib di firmware | Controller "bodoh" → **semua ditegakkan di Edge** (P9) | Device tak menyediakannya |
| V5 | Palang buka/tutup generik | **Buka via perintah, tutup dipicu sensor** (tepi turun LD) + interlock Edge | Jawaban klien |
| V6 | Dashboard tunggal | **Dashboard Pusat (cloud) vs Lokal (PC Admin)** | Topologi 3-tier |
| V7 | Zero-downtime implisit | **P8 eksplisit**: lahan jalan penuh tanpa internet | LAN lokal, Edge titik kritis |

**V2 & V4 adalah akar dari hampir seluruh keputusan Epik 1–3 di bawah.** Protokol tanpa CRC berarti
parser harus defensif sendiri; firmware tanpa interlock berarti Edge tak boleh salah sekali pun.

---

## 4. Driver controller A6/A9 — Epik 1 (K1–K12)

Paket [`internal/hardware/tcpctl/`](../services/edge-api/internal/hardware/tcpctl/).

### K1 — Parser membuang byte sampah, bukan mempercayai aliran
Protokol tak punya CRC maupun panjang, jadi parser wajib defensif sendiri (P3): buang byte sebelum
Header, resync saat Header baru muncul sebelum Footer, tolak frame kosong/kepanjangan/non-ASCII,
batasi buffer lewat `MaxCommandLen`.
**Harga:** frame sah yang kebetulan cacat ikut terbuang. Diterima — frame rusak yang ditelan diam-diam
jauh lebih berbahaya, dan tiap kejadian tercatat di `ParserStats`.
*Sumber: `frame.go`, commit `5850058`.*

### K2 — Frame masuk tak pernah dibuang saat konsumen lambat
Berbeda dari simulator yang membuang. Kehilangan event loop bawah = kehilangan dasar interlock (P4).
**Harga:** konsumen lambat menahan pompa frame. Itu memang yang diinginkan — lebih baik tertahan
daripada buta.
*Sumber: `client.go`, `gate.go:sebarkan`, commit `5850058`.*

### K3 — Backoff eksponensial **selalu** ber-jitter
Satu switch/UPS mati memutus seluruh gerbang di lahan bersamaan; tanpa jitter semuanya dial ulang
pada detik yang sama berulang kali. Equal jitter: setengah tetap + setengah acak, 0,5 s → maks 15 s.
*Sumber: `backoff.go`, commit `c87bb44`.*

### K4 — `Frames()` bertahan melintasi reconnect
`Client` mewakili satu koneksi dan kanalnya tertutup saat putus; `Device` tidak. State machine
gerbang tak perlu tahu soal gangguan koneksi.
*Sumber: `device.go`, commit `c87bb44`.*

### K5 — Perintah **tak pernah** diantre saat terputus ⚠️
`Send`/`Exec` menolak dengan `ErrNotConnected`. `OUT1OFF` yang tertahan lalu terkirim beberapa detik
kemudian bisa menutup palang di atas kendaraan lain (P4).
**Harga:** blip koneksi sesaat membatalkan perintah yang sebetulnya masih relevan.
**Status:** disempurnakan — bukan dibatalkan — oleh K27–K31 (task 3.3).
*Sumber: `device.go:ErrNotConnected`, commit `c87bb44`.*

### K6 — Tiga status device, bukan dua
`DISCONNECTED` (tak ada TCP) / `ONLINE` / `UNRESPONSIVE` (socket hidup, controller bisu). Kabel
tercabut dan controller hang butuh **tindakan perbaikan berbeda**; menggabungkannya membuat teknisi
mengejar hal yang salah.
*Sumber: `device.go`, commit `8c3fe82`.*

### K7 — Controller bisu → koneksi **diputus paksa**
Setelah 3× PING tanpa PINGOK. TCP setengah terbuka bisa bertahan menit-menit sebelum keep-alive
socket menyadarinya, dan selama itu Edge mengira palang bisa diperintah padahal tidak.
*Sumber: `device.go:keepalive`, commit `8c3fe82`.*

### K8 — Hanya `PINGOK` yang dihitung sebagai bukti hidup
Bukan sembarang lalu lintas masuk: controller yang hang masih mungkin memuntahkan event lama dari
buffer-nya. `WithPingInterval(0)` tersedia sebagai katup darurat bila controller lapangan ternyata
tak mematuhi kontrak PING (§13 masih menunggu vendor).
*Sumber: `device.go`, commit `8c3fe82`.*

### K9 — Retry hanya untuk "terkirim tapi tak dibalas"
Terputus/dibatalkan/ditutup tidak diulang — konsisten dengan K5. `maxAttempts` = jumlah percobaan
**total** (3), bukan 3 tambahan; "retry 3×" di PRD dibaca sebagai tiga kali kirim lalu menyerah.
*Sumber: `exec.go`, commit `31cc084`.*

### K10 — Interlock diperiksa ulang di **setiap** percobaan retry ⚠️
`WithCommandGuard` dijalankan sebelum tiap percobaan, bukan sekali di awal. Satu `Exec` merentang
ratusan milidetik dan loop bawah bisa berubah HIGH di tengahnya — `OUT1OFF` yang aman di percobaan
pertama bisa tak aman di percobaan ketiga.
*Sumber: `exec.go:WithCommandGuard`, commit `31cc084`.*

### K11 — Debounce berbasis **stabilitas**, bukan tepi pertama ⚠️
Perubahan diakui setelah nilainya bertahan sepanjang jendela (≥150 ms). Palang menutup pada tepi
**turun** loop bawah, jadi satu pantulan LOW palsu yang lolos bisa menutup palang di atas kendaraan
yang masih lewat.
**Harga:** pengakuan tertunda 150 ms. Tak terasa bagi pengemudi; meloloskan pantulan bisa mencederai.
*Sumber: `debounce.go`, commit `b5d3419`.*

### K12 — Dua pandangan sengaja dibedakan: mentah vs tepercaya
`Frames()` membawa `INxON/OFF` mentah untuk halaman Hardware & telemetry — teknisi justru ingin
melihat pantulan yang dibuang. `LoopEvents()` membawa yang sudah ter-debounce untuk state machine.
*Sumber: `device.go`, commit `b5d3419`.*

### K13 — `gates.config` menolak dua fungsi menunjuk kanal fisik sama
Bukan kerapian: `green_light` yang menunjuk relay sama dengan `barrier` berarti menyalakan lampu ikut
menggerakkan palang. Salah ketik tidak boleh berubah jadi palang yang bergerak sendiri, jadi ditolak
sejak startup alih-alih ditemukan di lapangan.
*Sumber: `gateconfig.go:Validate`, commit `4b2eec7`.*

### K14 — Bawaan mode palang `hold`, bukan `pulse`
Pada `pulse`, waktu menutup ditentukan mekanisme palang, **bukan Edge** — sehingga Edge tak dapat
menahan penutupan saat loop bawah HIGH. `hold` adalah satu-satunya mode yang membuat interlock P4
benar-benar dapat ditegakkan.
**Konsekuensi:** di lahan bermode `pulse`, interlock perangkat lunak **tidak berlaku**, dan itu
didokumentasikan apa adanya alih-alih disamarkan.
*Sumber: `gateconfig.go`, `gate.go:Barrier.Close`, commit `4b2eec7`.*

### K15 — Telemetry ring berukuran tetap, keepalive tak dicatat
Edge berjalan berbulan-bulan tanpa diawasi; jejak komunikasi yang tumbuh bebas menghabiskan memori
lahan. PING/PINGOK menghasilkan 2 entri/detik — membiarkannya masuk akan mengubur justru kejadian
yang ingin dilihat teknisi. `WithTelemetryKeepalive` menyalakannya saat mendiagnosa keepalive itu sendiri.
*Sumber: `telemetry.go`, commit `b2d63a1`.*

### K16 — Simulator controller bisa disuruh **berperilaku buruk**
`simdev` bukan cuma happy-path: `Diamkan()` membisu tanpa menutup socket (controller hang),
`PutusKoneksi()` memutus sepihak (kabel dicabut), `Pantul()` menghasilkan pantulan kontak. Jalur
pemulihan driver hanya benar-benar teruji kalau ada yang bisa merusaknya (P7).
*Sumber: `simdev/`, commit `b2d63a1`.*

---

## 5. Multi-gerbang & keselamatan Edge — Epik 2 (K17–K20)

Paket [`internal/gatesvc/`](../services/edge-api/internal/gatesvc/).

### K17 — Satu goroutine pemilik per gerbang, bukan mutex global ⚠️
Seluruh sentuhan ke state machine — memasukkan event maupun **membaca** state — lewat inbox gerbang
itu. Sebelumnya satu mutex menyerialkan semua gerbang, jadi satu gerbang tersendat menahan seluruh
lahan (melanggar P8).
**Jangan** menambahkan mutex global atau menyentuh state machine dari luar inbox-nya.
*Sumber: `gatesvc.go`, commit `d01bebd`.*

### K18 — `code` gerbang kembar menghentikan startup
`code` adalah kunci gerbang di event, endpoint, dan pencarian runner. Dua gerbang bercode sama berarti
perintah untuk satu diam-diam mengenai yang lain. Konfigurasi salah lebih baik menghentikan startup
daripada memunculkan gerbang berperilaku ganjil di lapangan.
*Sumber: `spec.go:ValidateSpecs`, commit `d01bebd`.*

### K19 — Setiap event membawa `gate_code` ⚠️
Sebelumnya hanya ada `"entry"`/`"exit"`, sehingga lahan dengan dua gerbang masuk tak punya cara
membedakan asal event. Jangan menambah event tanpa label itu.
*Sumber: `gatesvc.go:emit`, commit `d01bebd`.*

### K20 — Auto-close tidak ditambahkan
PRD v3 menyebut auto-close >60 dtk, tapi state machine **sudah** menutup sendiri lewat timeout
`no_show` (45 dtk) — lebih ketat, jadi syaratnya sudah terpenuhi. Menambah timer kedua untuk hal yang
sama hanya menghasilkan **dua pihak yang memerintah palang**.
*Sumber: `watchdog.go`, commit `b26af7f`.*

---

## 6. Ketahanan Edge — Epik 3 (K21–K26)

### K21 — Resync `STAT` hanya mengumumkan kanal **HIGH** ⚠️
LOW adalah keadaan istirahat: saat lahan sepi keempat kanal LOW, jadi mengumumkannya berarti
memancarkan empat tepi **turun** palsu di tiap reconnect — perintah tutup untuk kendaraan yang tak
ada. Jangan "melengkapi" resync dengan mengumumkan LOW.
*Sumber: `resync.go:tanamkanStat`, commit `5ce6da0`.*

### K22 — Potret `STAT` tak pernah menimpa kanal yang sudah diketahui ⚠️
Balasan `STAT` bisa tiba **setelah** event `INxON/INxOFF` yang lebih baru; menimpanya memundurkan
status ke masa lalu.
*Sumber: `debounce.go:Seed`, commit `5ce6da0`.*

### K23 — Resync gagal = Warning, tidak menjatuhkan koneksi
Semantik `STAT` masih menunggu konfirmasi vendor (H2), jadi controller yang belum patuh akan gagal di
*setiap* reconnect. Menjadikannya Critical berarti membanjiri alert dengan hal yang tak bisa
diperbaiki teknisi.
**Harga (jujur):** posisi loop kembali dipelajari dari event saja — persis perilaku sebelum 3.2 — dan
interlock tetap tidak memblokir saat status tak diketahui. `WithStatResync(false)` = katup darurat.
*Sumber: `resync.go`, commit `5ce6da0`.*

### K24 — Interlock **tidak** memblokir saat loop bawah tak diketahui
Memblokir tanpa dasar membuat palang menggantung terbuka setiap kali koneksi pulih, dan palang yang
tak pernah menutup adalah kegagalan operasional tersendiri. Resync (K21–K23) mempersempit jendela
"belum diketahui" tapi **tidak menghapusnya** — jangan mengubah cabang ini menjadi "blokir bila tak
diketahui" dengan anggapan resync selalu berhasil.
*Sumber: `gate.go:periksaInterlock`, commit `28f572b`.*

### K25 — Healthcheck memprobe dengan batas waktu, dan kanal hasilnya dibuffer ⚠️
`Runner.State()` menunggu inbox tanpa batas; dipakai untuk healthcheck ia menggantung persis pada
gerbang yang paling perlu dilaporkan. Probe punya batas waktu sendiri, dan "pemilik tak menjawab"
menjadi **hasil**, bukan kebuntuan.
Turunan yang halus tapi mematikan: tugas probe tetap tertinggal di inbox setelah kita menyerah. Kanal
hasil **wajib** dibuffer 1 — kalau tidak, goroutine pemilik memblokir selamanya saat mengirim ke kanal
tanpa pembaca, dan healthcheck yang dimaksudkan mendeteksi kemacetan justru menjadi penyebabnya.
*Sumber: `health.go:probe`, commit `740aa18`. Kedua invarian punya uji mutasi.*

### K26 — `status` di `/api/v1/health` tidak ikut memburuk saat gerbang mati
`status` = proses edge-api hidup. Kesehatan lahan dibaca dari `gates_status` yang terpisah. Pemantau
yang me-restart edge-api karena satu controller tercabut justru **menjatuhkan gerbang lain yang masih
sehat** (P8). Sebaliknya, `GET /api/v1/gates/:code/health` selalu 200 selama gerbangnya dikenal —
`down` adalah jawaban yang berhasil, bukan kegagalan permintaan.
*Sumber: `cmd/edge-api/server.go`, `gates.go`, commit `740aa18`.*

---

## 7. Antrian perintah per device — task 3.3 (K27–K31)

> **Status: terimplementasi** — `tcpctl/rekonsiliasi.go`, commit task 3.3.

Nama task-nya "antrian perintah", tapi setelah permukaan perintahnya dipetakan, masalahnya bukan
*kapan* mengirim ulang — melainkan *apa* yang dikirim ulang.

**Antrian mengulang keputusan dari masa lalu. Rekonsiliasi menegaskan keadaan sekarang.** Yang basi
persis adalah masa lalunya: `OUT1OFF` yang tertahan berbahaya bukan karena terlambat, tapi karena ia
membawa asumsi "tak ada kendaraan di bawah palang" yang sudah kedaluwarsa saat ia terkirim.

### K27 — Perlakuan ditentukan **semantik perintah**, bukan satu TTL seragam

| Kelas | Perintah | Perlakuan |
|---|---|---|
| Kejadian sesaat | `TRIG{palang}` (mode pulse) | **Tak pernah diantre.** Tak ada cara aman mengulang "buka" 3 detik telat. |
| Keadaan, keselamatan-kritis | `OUT{palang}ON/OFF` (mode hold) | **Direkonsiliasi** saat reconnect, interlock diperiksa pada detik itu. |
| Keadaan, tak kritis | lampu hijau/merah | **Direkonsiliasi**, latest-wins. Gelang kedip sudah menyembuhkan diri tiap 500 ms. |
| Kueri | `STAT` | Tak pernah diantre — resync (K21–K23) sudah memegangnya. |

### K28 — Daftar-putih, bukan daftar-hitam
Bawaannya **tidak** diantre. Perintah baru harus mendaftar eksplisit disertai alasan keselamatannya.
Sikap P3/P9: antrian itu bahaya, jadi keanggotaannya lewat pembuktian, bukan asumsi.

### K29 — Kedalaman 1 per kelas, latest-wins — bukan FIFO
Dua perintah palang yang mengantre sudah merupakan kontradiksi; yang lama tak pernah punya nilai.

### K30 — Rekonsiliasi penutupan menuntut bukti **positif** LOW ⚠️
Ini **lebih ketat** daripada jalur hidup (K24), dan asimetrinya disengaja:

> Jalur hidup boleh menutup saat loop tak diketahui karena ia punya bukti yang tak dimiliki
> rekonsiliator: ia baru saja **melihat LD2 turun**. Rekonsiliator tak melihat apa pun — ia hanya tahu
> "FSM ingin tertutup". Menutup atas dasar itu dengan loop tak diketahui berarti **menutup buta**.

Jadi rekonsiliasi-tutup hanya jalan bila loop bawah **diketahui LOW**. Bila tak diketahui → jangan
tutup, biarkan terbuka, bunyikan alert.

**Harga — diterima secara sadar:** di lahan dengan controller yang tak patuh `STAT` (H2), palang bisa
**menggantung terbuka setelah tiap reconnect** sampai ada event loop nyata yang mengisi status.
Palang menggantung terbuka = kerugian pendapatan; palang menutup di atas mobil = cedera. Nilai kedua
kegagalan itu tidak simetris, dan kita memilih yang bisa diganti uang.
Alternatifnya (menutup buta dengan alert keras) melanggar P4 secara sadar dan **harus** keputusan
manusia yang tercatat, bukan default. Ditawarkan ke Fatih 2026-08-13 → dijawab "lanjut apa adanya".

### K31 — Kedaluwarsa berlaku pada niat **buka**, bukan pada antrian; ambangnya 45 detik

**Koreksi terhadap rancangan awal.** Saat aturan ini pertama dirumuskan, angkanya 2 detik —
diturunkan dari `DefaultBackoffMin` 500 ms + equal jitter, yang menaruh dua percobaan dial pertama
di dalam ~1,5 detik. Itu benar **untuk model antrian perintah**: di sana yang kedaluwarsa adalah
perintah yang menunggu terkirim, dan batasnya memang harus sesempit "blip".

Model yang akhirnya dipakai bukan antrian melainkan rekonsiliasi (K27), dan di sana yang disimpan
bukan perintah melainkan **niat yang terus diperbarui lapisan atas**. Niat tak pernah "menunggu
terkirim" — ia selalu mewakili kehendak terkini FSM. Memberinya batas 2 detik akan menolak
penegasan ulang yang justru benar: reconnect 5 detik ke dalam satu siklus buka masih menghadapi
kendaraan yang sama, yang masih menunggu palang.

Yang sesungguhnya perlu dijaga adalah **FSM yang macet**: niat buka yang tak pernah diperbarui
karena tak ada lagi yang memperbaruinya. Ambangnya karena itu disamakan dengan timeout `no_show`
(45 dtk) — di atas itu FSM yang sehat sudah membatalkan sesinya sendiri, jadi niat yang lebih tua
hanya mungkin berasal dari FSM yang tak lagi berjalan. Lihat `DefaultMaxOpenIntentAge`.

Niat **tutup** tak punya kedaluwarsa: ia dijaga syarat yang lebih kuat (K30), dan "tertutup" adalah
keadaan istirahat yang tak menjadi salah karena waktu berlalu.

Setiap penegasan yang dilewati wajib tercatat — `DeviceStats.ReconcileSkipped` + telemetry.
Perintah yang hilang diam-diam adalah cara kita kehilangan waktu berhari-hari nanti.

---

## 7b. Menjalankan Edge sebagai service — task 3.1 (K32–K34)

### K32 — Kegagalan fatal WAJIB mematikan proses ⚠️
`Restart=always` — di systemd maupun NSSM — hanya menangkap proses yang **mati**. Proses yang tetap
hidup setelah kegagalan fatal terbaca "active (running)" selamanya, tidak pernah dinyalakan ulang,
dan gerbang berhenti melayani tanpa satu pun alarm berbunyi.

Karena itu `main` memakai pola `run() error` dan keluar dengan status bukan-nol untuk setiap
kegagalan fatal, dan port HTTP diikat **sebelum** melayani sehingga bind yang gagal menjadi
kegagalan startup yang jujur, bukan error di dalam goroutine.

**Ini memperbaiki bug nyata, bukan pengerasan spekulatif.** Sebelumnya `app.Listen` dipanggil di
dalam goroutine dan errornya hanya dicatat; diukur dengan port yang sudah dipakai, biner lama
**tidak pernah mati** — hidup terus tanpa HTTP sama sekali.
*Sumber: `cmd/edge-api/main.go`.*

### K33 — Watchdog membuktikan hidupnya MESIN INTERNAL, bukan sehatnya gerbang ⚠️
Ini penerapan langsung K26 pada lapisan proses. Gerbang yang controller-nya tercabut memang harus
terbaca `down`, tetapi watchdog tak boleh membunuh edge-api karenanya: restart tak menyambungkan
kabel, dan ia **menjatuhkan gerbang lain yang masih melayani** (P8).

Yang dijadikan bukti hidup adalah **gelang healthcheck internal yang masih menyapu tepat waktu**
(`gatesvc.MesinInternalHidup`), dengan ambang 4× interval — satu sapuan terlewat karena GC bukan
kebekuan, dan watchdog yang gugup lebih merusak daripada tak ada. Service yang belum `Start`
dinyatakan hidup, supaya watchdog tak membunuh proses tepat saat ia sedang bangun.
*Sumber: `gatesvc/health.go`, `cmd/edge-api/main.go:jalankanWatchdog`.*

### K34 — Windows memakai NSSM + watchdog eksternal, bukan kode `svc` native
Integrasi `golang.org/x/sys/windows/svc` akan menaruh kode khusus Windows di jalur startup yang
**tidak dapat kita uji sama sekali** — tak ada Windows di CI maupun di lingkungan pengembangan ini.
Kode yang hanya lolos kompilasi, di jalur startup Edge, bukan pertukaran yang baik.

NSSM menangani restart, direktori kerja, dan rotasi log tanpa menuntut apa pun dari biner kita
selain daur hidup yang benar (K32) — yang justru dapat diuji penuh di Linux.

**Harga yang dibayar dan ditutup:** NSSM tak punya padanan `WatchdogSec`, jadi proses yang membeku
tak tertangkap. Celah itu ditutup `deploy/windows/watchdog-edge-api.ps1` sebagai Scheduled Task yang
menguji endpoint kesehatan dari luar — dan konsisten dengan K33, ia menguji **apakah proses
menjawab**, bukan apa isi jawabannya.
*Sumber: `deploy/windows/`.*

---

## 7c. Pemulihan setelah restart — task 3.5 (K35–K37)

### K35 — Pemulihan LAYANAN tercapai; pemulihan DATA belum, dan tak bisa sampai Epik 5 ⚠️
NFR-2.3 (< 15 dtk) **terpenuhi dan terukur**: siklus penuh SIGTERM → berhenti → nyala →
`READY=1` adalah **2,01 dtk** terburuk dari 5 putaran, dan itu pun didominasi jeda
`RestartSec=2s`. Berhenti ~0,00 dtk, nyala-sampai-siap ~0,01 dtk. Alat ukurnya ada di repo:
`deploy/ukur-pemulihan.sh`.

**Tetapi yang pulih hanyalah kemampuan melayani, bukan datanya.** `edge-api` masih memakai
`memstore` in-process, jadi restart menghapus seluruh kendaraan yang sedang berada di dalam
lahan, tiket aktif, dan rantai audit berjalan. Kendaraan yang masuk sebelum restart tak
akan dikenali saat keluar.

Ini **tidak dapat diperbaiki di Epik 3**. Persistensi adalah task 5.1 (repository pgx),
yang terblokir Docker. Menuliskannya di sini supaya "NFR-2.3 terpenuhi" tidak pernah dibaca
sebagai "restart itu aman bagi transaksi berjalan" — dua hal yang sangat berbeda, dan
selisihnya adalah uang pelanggan.

> **Update 2026-08-16 — SELESAI.** Epik 5 tuntas (ternyata tak butuh Docker, lihat K44) dan
> pemulihan DATA kini terbukti untuk `EDGE_STORE=postgres` — lihat K46. Paragraf di atas
> dibiarkan apa adanya (bukan dihapus) karena mendokumentasikan keadaan yang BENAR pada
> saat ditulis; §5.4 kredo dokumen ini adalah rekaman keputusan, bukan status hidup. Yang
> berubah dicatat sebagai entri baru (K46), bukan menimpa yang lama.

### K36 — Palang yang ditinggalkan proses sebelumnya ditutup saat startup ⚠️
Relay controller mempertahankan posisinya melewati matinya edge-api. Proses yang mati saat
palang terangkat meninggalkan gerbang **terbuka selamanya**: proses baru memulai state
machine dari IDLE, tak punya kehendak apa pun untuk ditegaskan, dan tak satu pun pihak
menutupnya.

**Diukur sebelum ditulis.** Probe langsung terhadap simulator: setelah restart, relay palang
tetap `true` dan potret `STAT` melaporkannya apa adanya (`outputs=[true false false false]`,
`inputs` seluruhnya LOW) — pengetahuannya sudah ada, hanya tak ada yang bertindak.

Syaratnya sama ketatnya dengan K30 — bukti **positif** loop bawah LOW — ditambah satu:
potret `STAT` harus segar (≤ 10 dtk). Menutup atas dasar potret koneksi sebelumnya sama saja
menutup buta.

**Kenapa menutup, bukan sekadar membunyikan alarm:** palang menggantung terbuka adalah
kebocoran pendapatan tanpa batas sekaligus lubang keamanan, sedangkan menutup saat loop bawah
terbukti LOW secara fisik aman — tak ada yang di bawahnya. Alarm tetap dibunyikan
(`SeverityCritical`); ia melengkapi penutupan, bukan menggantikannya.
`SetCloseOrphanedBarrier(false)` = katup darurat.
*Sumber: `tcpctl/rekonsiliasi.go:tutupPalangYatim`.*

### K37 — Gerbang dirangkai SEBELUM koneksi dimulai ⚠️
`NewGate` memasang penjaga interlock **dan** rekonsiliator. Dipanggil setelah `dev.Start`,
koneksi pertama bisa terbentuk — beserta resync dan rekonsiliasinya — sebelum keduanya
terpasang.

Akibatnya bukan teoretis: pemeriksaan palang yatim (K36) hanya punya **satu** kesempatan,
yaitu koneksi pertama, dan kesempatan itu terlewat. Ditemukan karena uji K36 gagal; jalur
produksi (`gatesvc.buatPerangkatTCP`) ternyata punya urutan yang sama, sehingga rekonsiliasi
3.3 pun bisa terlewat di koneksi pertama.

Urutannya kini: `NewDevice` → `NewGate` → `Start`. Belum ada koneksi berarti belum ada yang
bisa terlewat. Prinsip yang sama menyelamatkan uji resync dari flake — rangkai dulu, baru
biarkan kejadian mengalir.
*Sumber: `gatesvc/perangkat.go`, `tcpctl/rekonsiliasi_test.go:restartProses`.*

---

## 7d. Chaos test — task 3.6 (K38)

### K38 — `LOCKED_NO_PAPER` tak punya jalan keluar sama sekali ⚠️ (diperbaiki)
Keadaan `LOCKED_NO_PAPER` **tidak muncul di `switch c.state`** pada `Controller.Handle`.
Sekali masuk, gerbang berhenti menerima event apa pun: tap member ditolak, tombol tak
berfungsi, dan gerbang tetap mati **walau kertas sudah diisi ulang** — hanya restart proses
yang memulihkannya.

Komentar di `issueTicket` sudah menjanjikan "kertas habis: casual berhenti, member tetap
bisa (§5.4.3)", tetapi janji itu tak pernah terwujud dalam kode. **D3 tidak berlaku di
lapangan selama ini.**

Ditemukan oleh chaos test 3.6 — bukan oleh pembacaan kode. Uji unit yang ada
(`TestPaperOutLocksCasual`) hanya memeriksa bahwa gerbang MASUK ke keadaan itu; tak ada yang
memeriksa apa yang terjadi sesudahnya. Itulah gunanya chaos test: uji unit memeriksa jalan
yang kita bayangkan, chaos test memeriksa yang kita lupakan.

Tiga jalan keluar ditambahkan:
- **tap member** → masuk tanpa tiket (D3, janji yang akhirnya ditepati)
- **tombol ditekan lagi** → `issueTicket` memeriksa ulang status printer; inilah jalur pulih
  setelah petugas mengisi kertas, tanpa restart
- **kendaraan pergi (LD1 turun)** → draft di-VOID, kembali `IDLE`. Tanpa ini gerbang tetap
  terkunci bagi kendaraan **berikutnya**, yang tak punya urusan dengan kertas habis.

*Sumber: `gate/entry.go:onLockedNoPaper`, `gate/entry_test.go`, `gatesvc/chaos_test.go`.*

---

## 7e. Persistensi pgx — task 5.1–5.4 (K39–K46)

### K39 — tenant_id/site_id diikat SEKALI di konstruksi `pgstore.New`, bukan per-panggilan
`gate.Store`/`gate.ExitStore`/dst. tak punya parameter tenant_id sama sekali — kontraknya
identik dengan memstore (task 5.1 menuntut ini). Enforcement §12.14 karenanya tak bisa lewat
tanda tangan metode; ia lewat konstruksi: `pgstore.New` meresolusi `TENANT_CODE`/`SITE_CODE`
jadi UUID sekali di awal, dan SETIAP query yang ditulis di paket ini memakai nilai yang sama.

**Harga:** satu proses `pgstore.Store` hanya bisa melayani SATU tenant+site — cocok dengan
realitas fisik (Edge = satu PC per lahan), tapi berarti repository ini secara sengaja tak bisa
dipakai ulang untuk skenario multi-tenant-per-proses (itu memang peran Cloud, bukan Edge).

### K40 — `audit.Chain` dipecah jadi `Next` (hitung, tak memajukan) + `Commit` (majukan setelah
tersimpan) ⚠️
`memstore.Store.Record` aman memakai `Chain.Append` (hitung+majukan sekaligus) karena
penyimpanannya in-memory — tak pernah "gagal tersimpan". `pgstore.Store.Record` bisa gagal di
tengah (pool putus, dll). Kalau chain sudah dimajukan SEBELUM baris benar-benar ter-INSERT,
kegagalan meninggalkan celah seq permanen di DB — `VerifyChain` berikutnya melaporkan "rusak"
untuk transaksi yang sebenarnya cuma gagal, bukan dimanipulasi (P5: negatif-palsu di jalur
anti-fraud sama buruknya dengan negatif-palsu di interlock keselamatan).

Urutan yang benar: `Next` (hitung entri, TAK mengubah state) → INSERT → `Commit` (majukan
state) HANYA setelah INSERT terbukti sukses. `Next` boleh dipanggil berulang tanpa `Commit` di
antaranya (retry aman, menghasilkan entri identik). `Chain.Append` dipertahankan sebagai
`Next`+`Commit` sekaligus — perilaku memstore tak berubah sedikit pun.

**Harga:** kalau proses mati TEPAT di antara INSERT sukses dan `Commit` (jendela sangat
sempit), percobaan `Record` berikutnya akan membentur `UNIQUE(node_id, seq)` di DB dan gagal
keras. Diterima dengan sengaja — lebih baik satu event audit gagal tercatat dengan jelas
(alert, log) daripada rantai diam-diam bercabang dua current_hash untuk seq yang sama.

### K41 — `created_at` dipotong ke presisi MIKROdetik sebelum dihash ⚠️ (ditemukan lewat uji nyata)
Formula hash (`chain.go`) memasukkan `created_at.Format(RFC3339Nano)` — presisi NANOdetik ala
Go. `timestamptz` PostgreSQL hanya menyimpan presisi mikrodetik. Akibatnya: hash dihitung saat
`Record` memakai timestamp presisi-nano, tapi saat baris yang sama dibaca balik dari DB untuk
`VerifyChain`, timestamp-nya sudah terpotong — `computeHash` ulang menghasilkan hash BEDA dari
yang tersimpan, dan `VerifyChain` melaporkan rantai rusak pada baca-ulang PERTAMA, walau tak
ada satu byte pun yang dimanipulasi siapa pun.

Ini TIDAK ditemukan lewat `go vet`/unit test (memstore tak pernah menulis-lalu-baca-ulang lewat
media dengan presisi berbeda) — ketahuan hanya karena uji integrasi dijalankan terhadap
PostgreSQL 16 sungguhan (lihat K43) dan `TestAuditChainSurvivesRestart` gagal telak. **Inilah
persis alasan chaos test/uji integrasi ada** (bandingkan K38): uji unit memeriksa jalan yang
kita bayangkan, uji terhadap sistem nyata memeriksa yang tak kita bayangkan sama sekali.

Perbaikan: `pgstore.Record` memotong `e.CreatedAt` ke `time.Microsecond` SEBELUM memanggil
`chain.Next` — hash yang dihitung sekarang PASTI sama dengan yang akan dihitung ulang dari
representasi yang benar-benar tersimpan. `audit.Chain` sendiri tak diubah (ia tetap DB-agnostic
by design); pemotongan ini murni tanggung jawab lapisan repository yang tahu presisi medianya.

### K42 — `Settle`/`Fail` di pgstore menyimpan rincian PENUH — sengaja BUKAN "parity" dengan memstore
`memstore.Settle`/`Fail` membuang seluruh `SettleInfo` (tendered, change, approval code, masked
PAN, dst.) — cuma mengubah status. Ini bukan kontrak yang harus ditiru persis, ini keterbatasan
in-memory (tak ada tempat menyimpannya di luar umur proses). Kolom-kolom itu ADA di skema
`payments` justru untuk ini (§6.2.1 masked PAN). `pgstore` menuliskannya penuh.

**Harga:** perilaku pgstore dan memstore kini beda secara OBSERVABLE untuk `PaymentViews`/dsb.
walau tanda tangan metodenya identik ("interface identik" task 5.1 berlaku untuk KONTRAK, bukan
untuk sejauh mana implementasi memory yang disederhanakan boleh menyembunyikan data). Membuang
data yang jelas-jelas diminta ditulis skema demi "konsistensi" dengan stub demo akan jadi
regresi diam-diam dari maksud PRD, bukan kepatuhan yang sah.

### K43 — `AuditEntries`/`VerifyChain` pgstore scan PENUH, sengaja TAK di-windowed
`/api/v1/health` memanggil `AuditEntries()` tiap request (dipoll monitoring). Godaan pertama:
batasi ke N baris terbaru (`ORDER BY seq DESC LIMIT n`) demi kecepatan. Ditolak: `audit.Verify`
menuntut rantai dimulai dari genesis (seq 1, `previous_hash` = `GenesisHash`) — jendela yang
dipotong TIDAK dimulai dari sana, jadi `Verify` akan SELALU melaporkan "rusak" di entri
pertama jendela (false positive terus-menerus), atau — kalau baseline-nya sekadar "dipercaya"
tanpa diverifikasi balik ke DB — celah verifikasi diam-diam (tampering di luar jendela tak
pernah terdeteksi). Dua-duanya lebih buruk daripada query yang lambat.

**Harga:** untuk lahan berumur sangat panjang, `/api/v1/health` bisa jadi query yang tumbuh
mahal. Diterima sebagai isu skala TERBUKA (bukan TODO tersembunyi di kode) — perbaikan yang
benar adalah verifikasi berbasis checkpoint (baseline seq+hash tersimpan terpisah), bukan
`LIMIT` polos. Dicatat di sini justru supaya siapa pun yang tergoda menambah `LIMIT` nanti
membaca alasan ini dulu.

### K44 — Sandbox pengerjaan session ini ternyata punya PostgreSQL 16 terpasang TANPA Docker
Asumsi awal (lihat riwayat sesi/PR sebelumnya): Epik 5 tertahan karena laptop developer tak
kuat menjalankan Docker. Ternyata sandbox tempat task 5.1–5.4 dikerjakan sudah punya paket
`postgresql-16` (server, bukan cuma `psql` client) terpasang lewat apt. Karena itu, task 5.1
ditest **ujung-ke-ujung terhadap Postgres sungguhan** — bukan cuma `go vet`/mock — termasuk
menjalankan biner `edge-api` penuh (`EDGE_STORE=postgres`) dan menggerakkan kendaraan lewat
gerbang simulator sampai baris `vehicles_log`/`audit_logs`/`sync_outbox` benar-benar tersimpan.

**Implikasi untuk developer lain:** kalau laptop dev tak kuat Docker, `apt install postgresql`
(paket server, bukan `postgresql-client` saja) adalah alternatif yang jauh lebih ringan untuk
Postgres LOKAL — tanpa image, tanpa container, layanan systemd biasa. Tak menggantikan Neon
untuk sesi kerja yang memang tanpa akses shell lokal (mis. sesi cloud dengan kebijakan
jaringan ketat, lihat catatan MCP Neon), tapi untuk sandbox/CI yang punya apt, ini pilihan
paling murah.

### K45 — CI mendapat Postgres lewat service container GitHub Actions, bukan Docker developer
`ci.yml` job `edge-api` sekarang punya `services: postgres:16` — Postgres milik RUNNER GitHub,
efemeral per run, dipakai untuk menjalankan migrasi goose (task 5.2) sungguhan lalu
`go test -tags=integration ./internal/pgstore/...`. Ini BUKAN Docker di mesin siapa pun —
developer tetap cukup `go build`/`go test` biasa (mode memory) di laptopnya; hanya CI yang
butuh Postgres, dan CI sudah presedennya menjalankan Docker untuk publish image (13.1).

**Harga:** uji integrasi pgstore TIDAK ikut jalan di `go test ./...` polos (perlu
`-tags=integration` eksplisit) — developer yang cuma jalan `go test` biasa tak akan melihat
kelas bug seperti K41 sampai push ke CI atau jalankan tag itu secara sadar terhadap Postgres
lokal/dev. Diterima: memaksa integration test selalu jalan di `go test` polos berarti SETIAP
developer mode-memory kudu punya Postgres tersambung, melanggar semangat D12 (mode memory ada
supaya edge-api bisa dikerjakan tanpa DB).

### K46 — K35 ditandai selesai: pemulihan DATA (task 3.5) dibuktikan lewat `TestVehicleDataSurvivesRestart`
K35 mencatat "yang pulih adalah LAYANAN, bukan DATA" dan menahan status 3.5 di 🔧. Sekarang
Epik 5 tuntas, klaim itu perlu DIBUKTIKAN, bukan diasumsikan otomatis benar hanya karena
`pgstore` ada — repository yang salah tulis kolom pun bisa "ada" tanpa benar-benar
menyelamatkan data.

Buktinya: `TestVehicleDataSurvivesRestart` (`internal/pgstore/integration_test.go`) —
kendaraan `CreateDraft`+`CommitInPremises` (persis di tengah sesi, bukan saat lahan kosong)
lewat `Store` pertama, `Store` itu DIBUANG sepenuhnya (bukan cuma di-`Close`, benar-benar tak
disentuh lagi), `Store` KEDUA dibuat dari `pool` yang sama meniru proses `edge-api` yang mati
lalu naik lagi, lalu `Lookup`+`Complete` lewat `Store` kedua harus berhasil seolah tak pernah
ada restart. Kalau ini lolos hanya karena `s1`/`s2` berbagi closure Go (bukan benar-benar lewat
DB), itu bukan bukti — makanya `s1` sengaja tak dipakai lagi setelah baris commit.

**Yang TETAP tidak berubah, sengaja:** mode `EDGE_STORE=memory` (bawaan, D12) MASIH kehilangan
semua data saat restart — itu bukan bug yang "ketinggalan diperbaiki", itu memang sifat
in-memory yang disengaja untuk demo/simulator. Klaim "data pulih" HANYA berlaku untuk mode
postgres, dan dokumen ini menegaskannya eksplisit supaya tak ada yang membaca 3.5 ✅ sebagai
"restart selalu aman" tanpa syarat.

*Sumber: `internal/pgstore/integration_test.go` (`TestVehicleDataSurvivesRestart`).*

*Sumber: `internal/pgstore/`, `internal/gatesvc/store.go`, `internal/audit/chain.go`
(`Next`/`Commit`), `internal/outbox/pg.go`, `db/migrations/00006_ticket_sequence.sql`,
`db/seed/dev_seed.sql`, `.github/workflows/ci.yml`.*

---

## 7f. Muat gerbang dari tabel `gates` — task 2.1, susulan Epik 5 (K47)

### K47 — Site tanpa gerbang aktif GAGAL KERAS, tak jatuh ke `DefaultSpecs`
`gatesvc.GateSource.LoadGates` versi `.env` (`SpecsFromConfig`) tak pernah bisa kosong — selalu
menghasilkan tepat 2 gerbang dari `GATE_IN_*`/`GATE_OUT_*`. Versi pgx (`pgstore.LoadGates`) BISA
kosong secara sah: site baru yang belum di-seed gerbangnya. Godaan pertama: jatuhkan ke
`DefaultSpecs()` (2 gerbang simulator) supaya lahan tetap "bisa jalan". Ditolak.

**Alasan:** lahan yang gagal start karena lupa seed gerbang adalah kegagalan yang JELAS — muncul
di log startup, operator langsung tahu ada yang salah. Lahan yang diam-diam jalan dengan 2
gerbang simulator karena tabelnya kosong adalah kegagalan SENYAP — semua terlihat baik-baik saja
sampai ada yang bertanya kenapa GATE-IN-02 yang seharusnya ada tak pernah muncul di dashboard.
Pola yang sama dengan K18 (code kembar menghentikan startup) dan K39 — konfigurasi yang salah
lebih baik menghentikan startup daripada melayani dengan asumsi diam-diam.

**Harga:** deploy pertama ke site baru WAJIB `db/seed/dev_seed.sql`-serupa (atau INSERT manual ke
`gates`) dijalankan LEBIH DULU sebelum `EDGE_STORE=postgres` bisa naik — satu langkah ekstra
dibanding mode memory yang selalu langsung jalan tanpa seed apa pun.

*Sumber: `internal/pgstore/gates.go`, `internal/pgstore/integration_test.go` (`TestLoadGates`).*

---

## 8. Penyimpangan tercatat dari PRD

Tempat implementasi sengaja tidak mengikuti spesifikasi, beserta alasannya.

| Dari | Spesifikasi | Yang dilakukan | Alasan |
|---|---|---|---|
| PRD v2 §5.4 | `VEHICLE_STALLED` → **lampu kuning blink** | `PatternRedBlink` | Peta pin v3 §5.6 tak menyediakan kanal lampu kuning — hanya hijau & merah. Merah berkedip adalah isyarat terdekat yang jujur; hijau akan menyesatkan pengemudi. Bila kuning ternyata ada di lapangan, tambahkan kanalnya ke `gates.config` lalu ganti polanya. |
| PRD v3 §6.2 | Auto-close palang **>60 dtk** | Tidak ditambahkan | FSM sudah menutup lewat `no_show` 45 dtk — lebih ketat (K20). |
| PRD Lampiran A | D11: interlock **redundan** PC + firmware | Hanya Edge | Controller A6/A9 tak menyediakan interlock sama sekali (V4). Bukan pilihan kita. |

---

## 9. Keputusan yang menunggu manusia

### Pertanyaan terbuka ke klien/vendor (PRD v3 §13)

| # | Pertanyaan | Memblokir |
|---|---|---|
| H1 | Protokol mesin tiket (merek? ESC/POS? USB/serial/TCP?) | Adapter printer (task 4.1) — gerbang masuk nyata belum lengkap tanpa ini |
| H2 | Semantik `STAT` & kekayaan fiturnya (klien: "belum tahu") | Rekonstruksi status saat reconnect — lihat K23, K30 |
| H3 | Model palang `hold` vs `pulse`? Auto-close mekanis? | Logika tutup §6.2 — lihat K14 |
| H4 | Scanner keluar: USB-HID wedge atau serial? Merek? | POS scanner |
| H5 | 1 controller = 1 gerbang? IP tiap controller statis? | Peta jaringan lahan |
| H6 | Kamera LPR per gerbang: RTSP tersedia? | LPR per gerbang |
| H7 | Sisa Q1/Q4/Q7/Q10 v2 (JTMO, anggaran, PIC lapangan) | Kontrak/pembayaran |

### Utang teknis yang diketahui

- ~~Tak ada lapisan basis data di `edge-api`~~ **SELESAI task 5.1–5.4** — `internal/pgstore`
  menggantikan `memstore` lewat `EDGE_STORE=postgres`, diuji terhadap Postgres 16 sungguhan
  (K39–K45). Yang MASIH belum tersambung ke DB: "muat daftar gerbang dari tabel `gates`" pada
  task 2.1 — `gatesvc.GateSource` masih baca dari `.env`/config statis, bukan query DB, walau
  tabelnya sudah ada & terisi lewat seed (K44). Itu task terpisah, bukan bagian 5.1–5.4.
- **`AuditEntries`/`VerifyChain` pgstore scan penuh tanpa jendela** (K43) — isu skala terbuka
  untuk lahan berumur sangat panjang, bukan bug, tapi juga bukan sudah "selesai selamanya".
- **Printer tiket belum punya adapter konkret** (H1). Gerbang masuk nyata memakai printer
  tersimulasi, diumumkan lewat `Runner.Disimulasikan()`, `/api/v1/gates`, dan status kesehatan
  `degraded` (K26) — perangkat palsu di jalur produksi tidak pernah tersamar sebagai sungguhan (P3).
- **`go test -race` tak bisa jalan di mesin dev Windows** (butuh cgo/gcc). CI Linux adalah gerbang
  race — jangan mengklaim bebas race berdasarkan uji lokal saja.

---

## 10. Riwayat dokumen

| Tanggal | Perubahan |
|---|---|
| 2026-08-18 | K47 ditambahkan (task 2.1 — muat gerbang dari tabel `gates`, susulan Epik 5). |
| 2026-08-16 | K46 ditambahkan (task 3.5 — pemulihan DATA dibuktikan `TestVehicleDataSurvivesRestart`; K35 ditandai selesai lewat addendum, bukan ditimpa). |
| 2026-08-16 | K39–K45 ditambahkan (task 5.1–5.4 — repository pgx, diuji terhadap Postgres 16 sungguhan tanpa Docker). Utang teknis "tak ada lapisan basis data" ditandai selesai. |
| 2026-08-13 | Dokumen dibuat. Merangkum D1–D12, V1–V7, K1–K31 dari inisialisasi monorepo sampai task 3.3. |
| 2026-08-13 | K38 ditambahkan (task 3.6 — chaos test menemukan LOCKED_NO_PAPER buntu). |
| 2026-08-13 | K35–K37 ditambahkan (task 3.5 — pemulihan, palang yatim, urutan perangkaian). |
| 2026-08-13 | K32–K34 ditambahkan (task 3.1 — service + watchdog). |
| 2026-08-13 | K27–K31 terimplementasi. K31 dikoreksi: ambang 2 dtk milik model antrian; model rekonsiliasi yang dipakai menuntut ambang yang berbeda (45 dtk, disamakan dengan `no_show`). |
