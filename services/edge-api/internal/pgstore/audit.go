package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/ids"
)

// ── gate.Auditor (rantai hash nyata, P5) ──

// Record menghitung entri berikutnya (audit.Chain.Next, TANPA memajukan state), menulisnya
// ke audit_logs, dan HANYA memajukan rantai (Commit) setelah INSERT terbukti sukses. Kalau
// INSERT gagal, Next berikutnya menghitung ulang seq yang sama — retry aman. Kalau proses
// mati tepat di antara INSERT sukses dan Commit, percobaan berikutnya akan membentur
// UNIQUE(node_id, seq) di DB dan gagal dengan jelas — lebih baik gagal keras daripada
// menyisipkan entri kembar diam-diam (lihat audit.Chain.Commit).
func (s *Store) Record(ctx context.Context, e audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	// current_hash memasukkan created_at (lihat rumus di audit.Chain) — TIMESTAMPTZ Postgres
	// hanya presisi mikrodetik, sedangkan time.Time Go presisi nanodetik. Potong SEBELUM
	// dihash, supaya hash yang dihitung sekarang sama dengan yang akan dihitung ULANG saat
	// baris ini dibaca-balik dari DB (AuditEntries → VerifyChain). Ditemukan lewat uji
	// integrasi terhadap Postgres sungguhan: tanpa ini, VerifyChain SELALU melaporkan rantai
	// rusak pada entri pertama walau tak ada satu pun yang dimanipulasi.
	e.CreatedAt = e.CreatedAt.Truncate(time.Microsecond)

	entry, err := s.chain.Next(e)
	if err != nil {
		return fmt.Errorf("pgstore: Record hash: %w", err)
	}
	payloadJSON, err := json.Marshal(entry.Event.Payload)
	if err != nil {
		return fmt.Errorf("pgstore: Record payload: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_logs (id, tenant_id, site_id, node_id, seq, event_type, severity,
			actor_id, actor_label, actor_role, gate_label, summary, payload,
			previous_hash, current_hash, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		ids.NewV7(), s.tenantID, s.siteID, entry.NodeID, entry.Seq,
		entry.Event.EventType, string(entry.Event.Severity),
		nullIfEmpty(entry.Event.ActorID), entry.Event.ActorLabel, entry.Event.ActorRole,
		nullIfEmpty(entry.Event.GateLabel), entry.Event.Summary, payloadJSON,
		entry.PreviousHash, entry.CurrentHash, entry.Event.CreatedAt)
	if err != nil {
		return fmt.Errorf("pgstore: Record insert: %w", err)
	}
	s.chain.Commit(entry)
	return nil
}

// AuditEntries mengembalikan SELURUH rantai audit node ini, terurut naik dari genesis — sama
// seperti memstore. Ini query yang tumbuh bersama umur node; dipertahankan scan penuh dengan
// sengaja demi kebenaran verifikasi rantai (lihat VerifyChain) alih-alih diam-diam dipotong.
// Kalau ini terbukti jadi beban nyata pada lahan berumur panjang, perbaikannya adalah jendela
// verifikasi berbasis checkpoint (baseline seq+hash tersimpan), BUKAN sekadar LIMIT — lihat
// CATATAN_KEPUTUSAN.md untuk alasan kenapa LIMIT polos berbahaya di sini.
//
// Tak menerima context (kontrak memstore.Store yang ditiru tak menyediakannya).
func (s *Store) AuditEntries() []audit.Entry {
	rows, err := s.pool.Query(context.Background(), `
		SELECT seq, event_type, severity, COALESCE(actor_id::text, ''), actor_label, actor_role,
		       COALESCE(gate_label, ''), summary, payload, previous_hash, current_hash, created_at
		  FROM audit_logs WHERE node_id = $1 ORDER BY seq ASC`,
		s.nodeID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []audit.Entry
	for rows.Next() {
		var e audit.Entry
		var payload []byte
		var severity string
		e.NodeID = s.nodeID
		if err := rows.Scan(&e.Seq, &e.Event.EventType, &severity, &e.Event.ActorID,
			&e.Event.ActorLabel, &e.Event.ActorRole, &e.Event.GateLabel, &e.Event.Summary,
			&payload, &e.PreviousHash, &e.CurrentHash, &e.Event.CreatedAt); err != nil {
			return out
		}
		e.Event.Severity = audit.Severity(severity)
		_ = json.Unmarshal(payload, &e.Event.Payload)
		out = append(out, e)
	}
	return out
}

// VerifyChain memverifikasi rantai penuh — lihat catatan skala di AuditEntries.
func (s *Store) VerifyChain() (int64, bool) {
	return audit.Verify(s.AuditEntries())
}
