//go:build integration

// Uji integrasi pgstore terhadap PostgreSQL SUNGGUHAN (task 5.1/5.2). Tak jalan di `go test`
// biasa — build tag "integration" harus eksplisit, dan EDGE_DATABASE_URL harus mengarah ke DB
// yang migrasinya (db/migrations, goose) sudah diterapkan:
//
//	goose -dir ../../../../db/migrations postgres "$EDGE_DATABASE_URL" up
//	EDGE_DATABASE_URL=... go test -tags=integration ./internal/pgstore/...
//
// Sengaja tak jalan di sandbox dev sesi ini (lihat CATATAN_KEPUTUSAN.md) — dirancang untuk
// sesi testing terpisah yang punya akses jaringan Postgres nyata (mis. Neon).
//
// Memakai DB apa adanya (tenant/site/tariff dibuat sendiri oleh test, dibersihkan di akhir)
// supaya aman dijalankan berulang terhadap DB dev yang sama tanpa migrasi ulang tiap kali.
package pgstore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
)

// shortRandom mengembalikan 8 karakter hex acak untuk fixture test (code/uid dsb.) — BUKAN
// ids.NewV7()[:8]. 8 karakter pertama UUIDv7 adalah bit TINGGI timestamp 48-bit, jadi identik
// untuk setiap pemanggilan dalam jendela ~65 detik yang sama (2^16 ms) — ditemukan lewat
// tabrakan node_id NYATA antar dua eksekusi `go test` yang berdekatan (lihat CATATAN_KEPUTUSAN
// K49): test run A menulis audit_logs dengan node_id "it-node-XXXXXXXX", test run B beberapa
// detik kemudian memakai node_id TEKS SAMA PERSIS untuk tenant yang beda sama sekali, dan
// query `WHERE node_id = $1` (scoping yang BENAR untuk kode produksi — node_id memang unik
// per perangkat fisik di dunia nyata) mengambil baris dari test run yang salah. 8 karakter
// TERAKHIR UUIDv7 (rand_b) genuinely acak, tak terikat waktu sama sekali.
func shortRandom() string {
	id := ids.NewV7()
	return id[len(id)-8:]
}

type testHandles struct {
	pool       *pgxpool.Pool
	tenantCode string
	siteCode   string
	tenantID   string
	siteID     string
	nodeID     string
	userID     string
}

func openTestStore(t *testing.T) (*Store, testHandles, func()) {
	t.Helper()
	dsn := os.Getenv("EDGE_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDGE_DATABASE_URL kosong — uji integrasi pgstore dilewati (lihat komentar berkas)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	tenantCode := "it-" + shortRandom()
	siteCode := "site-" + shortRandom()
	tenantID := ids.NewV7()
	siteID := ids.NewV7()
	nodeID := "it-node-" + shortRandom()

	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id, code, name) VALUES ($1, $2, 'IT Tenant')`,
		tenantID, tenantCode); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sites (id, tenant_id, code, name) VALUES ($1, $2, $3, 'IT Site')`,
		siteID, tenantID, siteCode); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO tariffs (id, site_id, vehicle_type, base_rate) VALUES ($1, $2, 'mobil', 5000)`,
		ids.NewV7(), siteID); err != nil {
		t.Fatalf("seed tariff: %v", err)
	}
	userID := ids.NewV7()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, password_hash, full_name, role)
		VALUES ($1, $2, $3, 'x', 'IT Kasir', 'Kasir')`,
		userID, tenantID, "it-"+shortRandom()+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	s, err := New(ctx, pool, tenantCode, siteCode, nodeID)
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}

	h := testHandles{
		pool: pool, tenantCode: tenantCode, siteCode: siteCode,
		tenantID: tenantID, siteID: siteID, nodeID: nodeID, userID: userID,
	}
	cleanup := func() {
		// audit_logs TIDAK dibersihkan — trigger append-only (P5) menolak DELETE apa pun,
		// termasuk dari test. Baris audit uji tertinggal permanen di DB dev; tak masalah untuk
		// korektnas (di-scope node_id yang acak per run, K49) — cuma numpuk di DB sandbox lokal.
		// audit_sync_outbox BUKAN append-only (ia antrean operasional, bukan rantai audit itu
		// sendiri — lihat db/migrations/00008) jadi AMAN dan MEMANG harus dibersihkan di sini.
		_, _ = pool.Exec(ctx, `DELETE FROM audit_sync_outbox WHERE node_id = $1`, nodeID)
		// Urutan mundur dari FK (payments/vehicles_log/shifts/tariffs/memberships/gates → sites → tenants).
		_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM vehicles_log WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM shifts WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM memberships WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tariffs WHERE site_id = $1`, siteID)
		_, _ = pool.Exec(ctx, `DELETE FROM ticket_counters WHERE site_id = $1`, siteID)
		_, _ = pool.Exec(ctx, `DELETE FROM devices WHERE site_id = $1`, siteID)
		_, _ = pool.Exec(ctx, `DELETE FROM gates WHERE site_id = $1`, siteID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM sites WHERE id = $1`, siteID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID)
		pool.Close()
	}
	return s, h, cleanup
}

// TestEntryToExitFlow — jalur penuh: draft → in_premises → lookup → complete, plus payment.
// Menegakkan §12.14: setiap baris yang ditulis harus terikat tenant_id/site_id repository ini.
func TestEntryToExitFlow(t *testing.T) {
	s, _, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	txID, err := s.CreateDraft(ctx)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	code, qr := s.Next()
	if code == "" || qr == "" {
		t.Fatal("Next() tiket kosong")
	}
	if err := s.CommitInPremises(ctx, txID, code, "D1234ZZ", ""); err != nil {
		t.Fatalf("CommitInPremises: %v", err)
	}

	found, err := s.Lookup(ctx, gate.LookupKey{Ticket: code})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !found.Found || found.ID != txID {
		t.Fatalf("Lookup tak menemukan tx yang baru di-commit: %+v", found)
	}

	rc, ok := s.Resolve("mobil")
	if !ok || rc.BaseRate != 5000 {
		t.Fatalf("Resolve tarif seed gagal: %+v ok=%v", rc, ok)
	}

	payID, err := s.Begin(ctx, txID, "CASH", 5000)
	if err != nil {
		t.Fatalf("Begin payment: %v", err)
	}
	if err := s.Settle(ctx, payID, gate.SettleInfo{Tendered: 10000, ChangeGiven: 5000}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	if err := s.Complete(ctx, txID, 5000, "D1234ZZ", nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	notFound, err := s.Lookup(ctx, gate.LookupKey{Ticket: code})
	if err != nil {
		t.Fatalf("Lookup pasca-complete: %v", err)
	}
	if notFound.Found {
		t.Fatal("kendaraan COMPLETED semestinya tak lagi ditemukan Lookup (yang mencari IN_PREMISES)")
	}
}

// TestMemberAntiPassback — tap masuk dua kali beruntun harus ditolak (§8.2), sama seperti
// memstore, tapi di sini lewat SELECT ... FOR UPDATE bukan mutex proses.
func TestMemberAntiPassback(t *testing.T) {
	s, _, cleanup := openTestStore(t)
	defer cleanup()

	uid := "IT-" + shortRandom()
	if id := s.AddMember(uid, []string{"D999XX"}, "mobil", time.Now().AddDate(0, 1, 0)); id == "" {
		t.Fatal("AddMember gagal (id kosong)")
	}

	d1, err := s.ValidateEntry(context.Background(), uid)
	if err != nil || !d1.Allowed {
		t.Fatalf("tap pertama harus diterima: %+v err=%v", d1, err)
	}
	d2, err := s.ValidateEntry(context.Background(), uid)
	if err != nil {
		t.Fatalf("ValidateEntry: %v", err)
	}
	if d2.Allowed || d2.Reason != "ANTIPASSBACK_VIOLATION" {
		t.Fatalf("tap kedua beruntun harus ANTIPASSBACK_VIOLATION, dapat %+v", d2)
	}
}

// TestVehicleDataSurvivesRestart — bukti langsung untuk task 3.5/K35: kendaraan yang MASUK
// sebelum "restart" (Store lama dibuang, Store baru dibuat dari pool yang sama, meniru proses
// edge-api yang benar-benar mati lalu naik lagi) harus tetap DIKENALI saat keluar. Dengan
// memstore ini MUSTAHIL lolos — datanya cuma ada di memori proses yang sudah tak ada lagi.
// Dengan pgstore, tak ada apa pun yang perlu "dipulihkan" — datanya tak pernah hilang.
func TestVehicleDataSurvivesRestart(t *testing.T) {
	s1, h, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Kendaraan masuk & DI TENGAH SESI (di dalam lahan) saat "restart" terjadi — skenario
	// persis yang diuji di 3.5: bukan restart saat lahan kosong.
	txID, err := s1.CreateDraft(ctx)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	code, _ := s1.Next()
	if err := s1.CommitInPremises(ctx, txID, code, "D9999RS", ""); err != nil {
		t.Fatalf("CommitInPremises: %v", err)
	}

	// "Restart": s1 dibuang sepenuhnya (tak dipakai lagi), Store baru dibuat dari pool yang
	// sama seperti main.go akan lakukan saat proses edge-api naik lagi. Tak ada state apa pun
	// yang dioper manual dari s1 ke s2 — kalau tes ini lolos HANYA karena s2 "mewarisi" s1
	// lewat closure Go, itu bukan bukti apa pun; makanya s1 sengaja tak disentuh lagi setelah
	// baris ini.
	s2, err := New(ctx, h.pool, h.tenantCode, h.siteCode, h.nodeID)
	if err != nil {
		t.Fatalf("New (simulasi restart): %v", err)
	}

	found, err := s2.Lookup(ctx, gate.LookupKey{Ticket: code})
	if err != nil {
		t.Fatalf("Lookup pasca-restart: %v", err)
	}
	if !found.Found || found.ID != txID {
		t.Fatalf("kendaraan yang masuk SEBELUM restart harus tetap dikenali SESUDAHNYA — "+
			"inilah persis yang gagal di memstore (K35). Lookup: %+v", found)
	}

	// Kendaraan itu harus bisa KELUAR normal lewat Store yang baru — bukan cuma "terlihat".
	if err := s2.Complete(ctx, txID, 5000, "D9999RS", nil); err != nil {
		t.Fatalf("Complete pasca-restart: %v", err)
	}
	gone, err := s2.Lookup(ctx, gate.LookupKey{Ticket: code})
	if err != nil {
		t.Fatalf("Lookup setelah Complete: %v", err)
	}
	if gone.Found {
		t.Fatal("kendaraan yang sudah Complete semestinya tak lagi IN_PREMISES")
	}
}

// TestAuditChainSurvivesRestart — Record menegakkan rantai lewat "restart" proses: New()
// kedua atas tenant/site/node yang sama harus melanjutkan seq dari DB, bukan mulai dari
// genesis lagi (kalau tidak, baris berikutnya akan membentur UNIQUE(node_id,seq) atau —
// lebih buruk — diam-diam menulis seq kembar dengan hash yang beda dasar).
func TestAuditChainSurvivesRestart(t *testing.T) {
	s1, h, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s1.Record(ctx, audit.Event{
			EventType: "TEST_EVENT", Severity: audit.SevNormal, ActorLabel: "test", ActorRole: "System",
			Summary: "uji integrasi",
		}); err != nil {
			t.Fatalf("Record %d (s1): %v", i, err)
		}
	}
	if broken, ok := s1.VerifyChain(); !ok {
		t.Fatalf("rantai s1 harus utuh, rusak di seq %d", broken)
	}

	// "Restart": Store baru dari pool yang sama, tenant/site/node sama.
	s2, err := New(ctx, h.pool, h.tenantCode, h.siteCode, h.nodeID)
	if err != nil {
		t.Fatalf("New (simulasi restart): %v", err)
	}
	if err := s2.Record(ctx, audit.Event{
		EventType: "TEST_EVENT_AFTER_RESTART", Severity: audit.SevNormal,
		ActorLabel: "test", ActorRole: "System", Summary: "setelah restart",
	}); err != nil {
		t.Fatalf("Record setelah restart: %v", err)
	}
	entries := s2.AuditEntries()
	if len(entries) != 4 {
		t.Fatalf("harus ada 4 entri (3 sebelum + 1 sesudah restart), dapat %d", len(entries))
	}
	if broken, ok := s2.VerifyChain(); !ok {
		t.Fatalf("rantai gabungan harus tetap utuh lintas restart, rusak di seq %d", broken)
	}
}

// TestAuditOutboxTransactionalWithAuditLog — task 8.5: Record() menulis audit_logs DAN
// audit_sync_outbox dalam SATU transaksi. Menguji siklus penuh terhadap Postgres sungguhan:
// enqueue otomatis lewat Record, fetch (urutan seq, bukan urutan id baris), mark-sent
// menghapusnya dari antrean pending, dan mark-failed TAK PERNAH membuatnya permanen gagal.
func TestAuditOutboxTransactionalWithAuditLog(t *testing.T) {
	s, _, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if n := s.AuditOutbox().PendingAuditCount(); n != 0 {
		t.Fatalf("outbox harus kosong di awal, got %d", n)
	}

	for i := 0; i < 3; i++ {
		if err := s.Record(ctx, audit.Event{
			EventType: "TEST_OUTBOX_EVENT", Severity: audit.SevNormal,
			ActorLabel: "test", ActorRole: "System", Summary: "uji outbox audit",
		}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	// Tiap baris audit_logs harus punya padanan persis di audit_sync_outbox — jumlah sama,
	// bukan cuma "outbox tak kosong" (buktikan keduanya benar-benar satu transaksi, P1/D4).
	if n := s.AuditOutbox().PendingAuditCount(); n != 3 {
		t.Fatalf("harus 3 item pending (satu per Record), got %d", n)
	}

	items := s.AuditOutbox().FetchPendingAudit(10)
	if len(items) != 3 {
		t.Fatalf("FetchPendingAudit harus 3, got %d", len(items))
	}
	for i, it := range items {
		wantSeq := int64(i + 1)
		if it.Seq != wantSeq {
			t.Fatalf("urutan seq salah di posisi %d: got %d, want %d", i, it.Seq, wantSeq)
		}
		if it.Payload["event_type"] != "TEST_OUTBOX_EVENT" {
			t.Fatalf("payload event_type hilang: %+v", it.Payload)
		}
		if it.Payload["current_hash"] == "" || it.Payload["current_hash"] == nil {
			t.Fatalf("payload harus membawa current_hash untuk verifikasi rantai di Cloud: %+v", it.Payload)
		}
	}

	// Kirim 2 dari 3 → sisa 1 pending, dan yang terkirim tak muncul lagi.
	s.AuditOutbox().MarkAuditSent([]int64{items[0].ID, items[1].ID})
	remaining := s.AuditOutbox().FetchPendingAudit(10)
	if len(remaining) != 1 || remaining[0].Seq != 3 {
		t.Fatalf("harus tersisa 1 item (seq 3), got %+v", remaining)
	}

	// MarkAuditFailed berulang TAK PERNAH memindahkan status ke FAILED permanen (beda dari
	// outbox.PG biasa yang punya batas percobaan) — item seq 3 harus tetap muncul di
	// FetchPendingAudit walau attempts-nya tinggi.
	for i := 0; i < 6; i++ {
		s.AuditOutbox().MarkAuditFailed(remaining[0].ID, "simulasi cloud tak terjangkau")
	}
	stillPending := s.AuditOutbox().FetchPendingAudit(10)
	if len(stillPending) != 1 || stillPending[0].Seq != 3 {
		t.Fatalf("item harus tetap pending setelah banyak kegagalan (retry selamanya, task 8.5): %+v", stillPending)
	}
	if stillPending[0].Attempts != 6 {
		t.Fatalf("attempts harus 6, got %d", stillPending[0].Attempts)
	}
	if n := s.AuditOutbox().PendingAuditCount(); n != 1 {
		t.Fatalf("PendingAuditCount harus 1, got %d", n)
	}
}

// TestLoadGates — task 2.1: daftar gerbang datang dari tabel `gates`, bukan `.env`. Menguji
// tiga hal sekaligus: (1) hanya gerbang `active` yang termuat, (2) urutannya sesuai `code`
// (stabil lintas restart), (3) site KOSONG gerbang gagal keras, bukan diam-diam jatuh ke
// default simulator — supaya "lupa seed" tak pernah tersamar sebagai "lahan demo sengaja".
func TestLoadGates(t *testing.T) {
	s, h, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Site baru dari openTestStore belum punya gerbang sama sekali — LoadGates harus gagal.
	if _, err := s.LoadGates(ctx); err == nil {
		t.Fatal("LoadGates atas site tanpa gerbang harus gagal, bukan diam-diam kosong")
	}

	if _, err := h.pool.Exec(ctx, `
		INSERT INTO gates (id, site_id, code, kind, controller_addr, transport, endpoint, status) VALUES
			($1, $4, 'GATE-IN-01', 'ENTRY', 1, 'sim', '', 'active'),
			($2, $4, 'GATE-OUT-01', 'EXIT', 2, 'sim', '', 'active'),
			($3, $4, 'GATE-IN-02-NONAKTIF', 'ENTRY', 3, 'sim', '', 'inactive')`,
		ids.NewV7(), ids.NewV7(), ids.NewV7(), h.siteID); err != nil {
		t.Fatalf("seed gates: %v", err)
	}

	specs, err := s.LoadGates(ctx)
	if err != nil {
		t.Fatalf("LoadGates: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("harus cuma 2 gerbang active (yang inactive tak ikut termuat), dapat %d: %+v",
			len(specs), specs)
	}
	if specs[0].Code != "GATE-IN-01" || specs[1].Code != "GATE-OUT-01" {
		t.Fatalf("urutan harus menaik berdasar code, dapat %+v", specs)
	}
	if specs[0].Kind != gatesvc.KindEntry || specs[1].Kind != gatesvc.KindExit {
		t.Fatalf("kind tak sesuai kolom `kind`, dapat %+v", specs)
	}
}

// TestShiftReconciliation — task 7.4: buka shift, payment lewat metode campuran (CASH,
// EDC_DEBIT, QRIS) otomatis tertaut ke shift yang sedang terbuka, tutup shift menghitung
// total per metode & selisih sesuai rumus §6.4. Juga menguji tiga pagar: shift kedua tak
// boleh dibuka selagi satu masih terbuka (unique index DB, K48), selisih ≠ 0 wajib note,
// dan shift yang sudah tertutup tak bisa ditutup lagi.
func TestShiftReconciliation(t *testing.T) {
	s, h, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	shiftID, err := s.OpenShift(ctx, h.userID, 100_000)
	if err != nil {
		t.Fatalf("OpenShift: %v", err)
	}

	if _, err := s.OpenShift(ctx, h.userID, 0); err == nil {
		t.Fatal("OpenShift kedua selagi satu masih terbuka harus ditolak (K48)")
	}

	settle := func(method string, amount int64) {
		t.Helper()
		txID, err := s.CreateDraft(ctx)
		if err != nil {
			t.Fatalf("CreateDraft: %v", err)
		}
		code, _ := s.Next()
		if err := s.CommitInPremises(ctx, txID, code, "D-SHIFT", ""); err != nil {
			t.Fatalf("CommitInPremises: %v", err)
		}
		payID, err := s.Begin(ctx, txID, method, amount)
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := s.Settle(ctx, payID, gate.SettleInfo{}); err != nil {
			t.Fatalf("Settle: %v", err)
		}
	}
	settle(gate.MethodCash, 50_000)
	settle(gate.MethodEDCDebit, 30_000)
	settle(gate.MethodQRIS, 20_000)

	// Selisih 0: dilaporkan persis kas awal + tunai sistem (100_000 + 50_000).
	report, err := s.CloseShift(ctx, shiftID, 150_000, "")
	if err != nil {
		t.Fatalf("CloseShift: %v", err)
	}
	if report.Status != "CLOSED" {
		t.Fatalf("selisih 0 harus CLOSED, dapat %+v", report)
	}
	if report.SystemCash != 50_000 || report.SystemEDC != 30_000 || report.SystemQRIS != 20_000 {
		t.Fatalf("jumlah per metode salah: %+v", report)
	}
	if report.Variance == nil || *report.Variance != 0 {
		t.Fatalf("variance harus 0, dapat %+v", report.Variance)
	}

	if _, err := s.CloseShift(ctx, shiftID, 150_000, ""); err == nil {
		t.Fatal("menutup shift yang sudah tertutup harus gagal")
	}

	// Shift kedua: selisih ≠ 0 tanpa note harus ditolak, lalu diterima dengan note.
	shift2, err := s.OpenShift(ctx, h.userID, 0)
	if err != nil {
		t.Fatalf("OpenShift kedua (setelah yang pertama tertutup): %v", err)
	}
	settle(gate.MethodCash, 10_000)
	if _, err := s.CloseShift(ctx, shift2, 5_000, ""); err == nil {
		t.Fatal("selisih != 0 tanpa note harus ditolak")
	}
	report2, err := s.CloseShift(ctx, shift2, 5_000, "kasir salah hitung kembalian")
	if err != nil {
		t.Fatalf("CloseShift dengan note: %v", err)
	}
	if report2.Status != "VARIANCE" || report2.Variance == nil || *report2.Variance != -5_000 {
		t.Fatalf("selisih -5000 harus VARIANCE, dapat %+v", report2)
	}

	views := s.ShiftViews()
	if len(views) != 2 {
		t.Fatalf("ShiftViews harus 2 shift, dapat %d", len(views))
	}
}
