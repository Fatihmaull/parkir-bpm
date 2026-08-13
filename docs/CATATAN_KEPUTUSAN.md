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

- **Tak ada lapisan basis data di `edge-api`.** Tak ada pgx di `go.mod`; `config.Store` menyebut
  `memory|postgres` tapi hanya `memory` yang jalan. Memblokir Epik 5 (kecuali 5.5) dan bagian "muat
  gerbang dari DB" pada task 2.1. `gatesvc.GateSource` adalah tempat sambungnya.
- **Printer tiket belum punya adapter konkret** (H1). Gerbang masuk nyata memakai printer
  tersimulasi, diumumkan lewat `Runner.Disimulasikan()`, `/api/v1/gates`, dan status kesehatan
  `degraded` (K26) — perangkat palsu di jalur produksi tidak pernah tersamar sebagai sungguhan (P3).
- **`go test -race` tak bisa jalan di mesin dev Windows** (butuh cgo/gcc). CI Linux adalah gerbang
  race — jangan mengklaim bebas race berdasarkan uji lokal saja.

---

## 10. Riwayat dokumen

| Tanggal | Perubahan |
|---|---|
| 2026-08-13 | Dokumen dibuat. Merangkum D1–D12, V1–V7, K1–K31 dari inisialisasi monorepo sampai task 3.3. |
| 2026-08-13 | K32–K34 ditambahkan (task 3.1 — service + watchdog). |
| 2026-08-13 | K27–K31 terimplementasi. K31 dikoreksi: ambang 2 dtk milik model antrian; model rekonsiliasi yang dipakai menuntut ambang yang berbeda (45 dtk, disamakan dengan `no_show`). |
