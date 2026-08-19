package store

import (
	"testing"
	"time"
)

// mkEntry membangun satu map[string]any siap dikirim ke ApplyAuditBatch, dengan hash yang
// benar-benar dihitung (bukan asal isi) — supaya tes ini menguji verifikasi kriptografisnya,
// bukan cuma bookkeeping seq.
func mkEntry(t *testing.T, nodeID string, seq int64, prevHash string, at time.Time) map[string]any {
	t.Helper()
	payload := map[string]any{"gate": "IN-1"}
	hash, err := computeHash(prevHash, nodeID, seq, "GATE_OPEN", "op-1", payload, at)
	if err != nil {
		t.Fatalf("computeHash: %v", err)
	}
	return map[string]any{
		"tenant_id": "t1", "site_id": "s1", "node_id": nodeID, "seq": float64(seq),
		"event_type": "GATE_OPEN", "severity": "normal", "actor_id": "op-1",
		"actor_label": "Operator 1", "actor_role": "Kasir", "gate_label": "IN-1",
		"summary": "buka palang", "payload": payload,
		"previous_hash": prevHash, "current_hash": hash,
		"created_at": at.Format(time.RFC3339Nano),
	}
}

func TestApplyAuditBatchContiguousChainAccepted(t *testing.T) {
	s := New()
	now := time.Now()
	e1 := mkEntry(t, "node-a", 1, GenesisHash, now)
	e2 := mkEntry(t, "node-a", 2, e1["current_hash"].(string), now.Add(time.Second))

	res := s.ApplyAuditBatch("t1", []map[string]any{e1, e2})
	if res.Applied != 2 || res.Skipped != 0 || len(res.Broken) != 0 {
		t.Fatalf("hasil tak terduga: %+v", res)
	}

	brokenSeq, ok, checked := s.VerifyAuditChain("t1", "node-a")
	if !ok || brokenSeq != 0 || checked != 2 {
		t.Fatalf("verifikasi gagal: brokenSeq=%d ok=%v checked=%d", brokenSeq, ok, checked)
	}
}

func TestApplyAuditBatchGapRejectedNotSkipped(t *testing.T) {
	s := New()
	now := time.Now()
	e1 := mkEntry(t, "node-a", 1, GenesisHash, now)
	// e2 dilewati — kirim langsung seq 3, mensimulasikan entri seq 2 belum tiba.
	e3 := mkEntry(t, "node-a", 3, "hash-seq-2-tak-pernah-ada", now.Add(2*time.Second))

	res := s.ApplyAuditBatch("t1", []map[string]any{e1, e3})
	if res.Applied != 1 {
		t.Fatalf("hanya seq 1 yang harus diterima, dapat applied=%d", res.Applied)
	}
	if len(res.Broken) != 1 {
		t.Fatalf("seq 3 (celah) harus ditolak, broken=%v", res.Broken)
	}

	// Checkpoint TIDAK boleh maju ke seq 3 — entri celah tak pernah menggerakkan lastSeq.
	chains := s.AuditChains("t1")
	if len(chains) != 1 || chains[0].LastSeq != 1 {
		t.Fatalf("checkpoint bergerak walau ada celah: %+v", chains)
	}
}

func TestApplyAuditBatchHashMismatchRejected(t *testing.T) {
	s := New()
	now := time.Now()
	e1 := mkEntry(t, "node-a", 1, GenesisHash, now)
	e1["current_hash"] = "sengaja-dirusak"

	res := s.ApplyAuditBatch("t1", []map[string]any{e1})
	if res.Applied != 0 || len(res.Broken) != 1 {
		t.Fatalf("entri dengan hash tak cocok harus ditolak, dapat: %+v", res)
	}
}

func TestApplyAuditBatchIdempotentRetry(t *testing.T) {
	s := New()
	now := time.Now()
	e1 := mkEntry(t, "node-a", 1, GenesisHash, now)

	res1 := s.ApplyAuditBatch("t1", []map[string]any{e1})
	if res1.Applied != 1 {
		t.Fatalf("kiriman pertama harus applied=1, dapat %+v", res1)
	}

	// Kirim ulang batch yang sama persis (mensimulasikan retry setelah network gagal
	// sebelum ACK) — harus dilewati sebagai duplikat aman, BUKAN dianggap error.
	res2 := s.ApplyAuditBatch("t1", []map[string]any{e1})
	if res2.Applied != 0 || res2.Skipped != 1 || len(res2.Broken) != 0 {
		t.Fatalf("retry identik harus di-skip idempoten, dapat: %+v", res2)
	}
}

func TestApplyAuditBatchRetryWithDifferentHashRejected(t *testing.T) {
	s := New()
	now := time.Now()
	e1 := mkEntry(t, "node-a", 1, GenesisHash, now)
	s.ApplyAuditBatch("t1", []map[string]any{e1})

	// seq sama, tapi isi (dan karenanya hash) berbeda dari yang sudah tersimpan — ini BUKAN
	// retry aman, melainkan indikasi manipulasi/korupsi; harus ditolak, bukan dilewati.
	tampered := mkEntry(t, "node-a", 1, GenesisHash, now.Add(time.Hour))

	res := s.ApplyAuditBatch("t1", []map[string]any{tampered})
	if res.Skipped != 0 || len(res.Broken) != 1 {
		t.Fatalf("retry dengan hash berbeda harus ditolak, dapat: %+v", res)
	}
}

func TestAuditChainsScopedByTenant(t *testing.T) {
	s := New()
	now := time.Now()
	e1 := mkEntry(t, "node-a", 1, GenesisHash, now)
	s.ApplyAuditBatch("t1", []map[string]any{e1})

	if chains := s.AuditChains("t2"); len(chains) != 0 {
		t.Fatalf("tenant lain tak boleh melihat rantai node ini: %+v", chains)
	}
	if entries := s.AuditEntries("t2", "node-a"); entries != nil {
		t.Fatalf("tenant lain tak boleh membaca entri node ini: %+v", entries)
	}
}
