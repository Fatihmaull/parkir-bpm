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
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
)

type testHandles struct {
	pool       *pgxpool.Pool
	tenantCode string
	siteCode   string
	tenantID   string
	nodeID     string
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

	tenantCode := "it-" + ids.NewV7()[:8]
	siteCode := "site-" + ids.NewV7()[:8]
	tenantID := ids.NewV7()
	siteID := ids.NewV7()
	nodeID := "it-node-" + ids.NewV7()[:8]

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

	s, err := New(ctx, pool, tenantCode, siteCode, nodeID)
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}

	h := testHandles{pool: pool, tenantCode: tenantCode, siteCode: siteCode, tenantID: tenantID, nodeID: nodeID}
	cleanup := func() {
		// Urutan mundur dari FK (audit_logs/payments/vehicles_log/tariffs/memberships → sites → tenants).
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM vehicles_log WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM memberships WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tariffs WHERE site_id = $1`, siteID)
		_, _ = pool.Exec(ctx, `DELETE FROM ticket_counters WHERE site_id = $1`, siteID)
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

	uid := "IT-" + ids.NewV7()[:8]
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
