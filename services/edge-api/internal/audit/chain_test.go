package audit

import (
	"testing"
	"time"
)

func sampleEvent(i int) Event {
	return Event{
		TenantID: "t1", SiteID: "s1", EventType: "GATE_OPEN", Severity: SevCritical,
		ActorID: "u1", ActorLabel: "Kasir A", ActorRole: "Kasir",
		Summary: "buka manual", Payload: map[string]any{"gate": "IN-01", "n": i},
		CreatedAt: time.Date(2026, 7, 24, 10, i, 0, 0, time.UTC),
	}
}

func buildChain(t *testing.T, n int) ([]Entry, *Chain) {
	t.Helper()
	c := NewChain("node-1", 0, "")
	var entries []Entry
	for i := 1; i <= n; i++ {
		e, err := c.Append(sampleEvent(i))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		entries = append(entries, e)
	}
	return entries, c
}

func TestGenesisAndSequence(t *testing.T) {
	entries, _ := buildChain(t, 3)
	if entries[0].PreviousHash != GenesisHash {
		t.Fatalf("entri pertama harus menyambung ke genesis")
	}
	for i, e := range entries {
		if e.Seq != int64(i+1) {
			t.Fatalf("seq tidak monoton: %d", e.Seq)
		}
		if i > 0 && e.PreviousHash != entries[i-1].CurrentHash {
			t.Fatalf("previous_hash entri %d tidak menyambung", i)
		}
	}
}

func TestVerifyIntactChain(t *testing.T) {
	entries, _ := buildChain(t, 5)
	if broken, ok := Verify(entries); !ok {
		t.Fatalf("rantai utuh harus lolos verifikasi, rusak di seq %d", broken)
	}
}

func TestVerifyDetectsTamperedPayload(t *testing.T) {
	entries, _ := buildChain(t, 5)
	// Ubah payload entri ke-3 tanpa menghitung ulang hash → harus terdeteksi.
	entries[2].Event.Payload["n"] = 999
	broken, ok := Verify(entries)
	if ok || broken != 3 {
		t.Fatalf("modifikasi payload harus terdeteksi di seq 3, got broken=%d ok=%v", broken, ok)
	}
}

func TestVerifyDetectsBrokenLink(t *testing.T) {
	entries, _ := buildChain(t, 5)
	// Rusak tautan: ganti previous_hash entri ke-4.
	entries[3].PreviousHash = "deadbeef"
	broken, ok := Verify(entries)
	if ok || broken != 4 {
		t.Fatalf("tautan rusak harus terdeteksi di seq 4, got broken=%d ok=%v", broken, ok)
	}
}

// TestNextDoesNotAdvanceChain — task 5.1: repository pgx harus bisa menghitung entri
// berikutnya berulang kali tanpa memajukan state selama INSERT belum terbukti sukses.
func TestNextDoesNotAdvanceChain(t *testing.T) {
	c := NewChain("node-1", 0, "")
	e1, err := c.Next(sampleEvent(1))
	if err != nil {
		t.Fatalf("next 1: %v", err)
	}
	// Panggil Next lagi tanpa Commit — harus menghasilkan entri identik (seq belum maju).
	e2, err := c.Next(sampleEvent(1))
	if err != nil {
		t.Fatalf("next 2: %v", err)
	}
	if e1.Seq != e2.Seq || e1.CurrentHash != e2.CurrentHash || e1.PreviousHash != e2.PreviousHash {
		t.Fatalf("Next tanpa Commit di antaranya harus deterministik/idempoten, dapat %+v lalu %+v", e1, e2)
	}
	if c.lastSeq != 0 {
		t.Fatalf("Next tak boleh memajukan lastSeq, dapat %d", c.lastSeq)
	}
}

// TestCommitAdvancesThenNextMovesOn — Commit yang berhasil memajukan chain, sehingga
// Next berikutnya menghitung seq baru, bukan mengulang.
func TestCommitAdvancesThenNextMovesOn(t *testing.T) {
	c := NewChain("node-1", 0, "")
	e1, _ := c.Next(sampleEvent(1))
	c.Commit(e1)
	e2, _ := c.Next(sampleEvent(2))
	if e2.Seq != 2 || e2.PreviousHash != e1.CurrentHash {
		t.Fatalf("setelah Commit, Next harus lanjut dari entri ter-commit, dapat %+v", e2)
	}
}

// TestCommitOutOfOrderPanics — Commit atas entry yang bukan lanjutan langsung adalah bug
// pemanggil (mis. melewati Commit lalu Commit entri lama) dan harus gagal keras, bukan diam
// diam merusak rantai (P5).
func TestCommitOutOfOrderPanics(t *testing.T) {
	c := NewChain("node-1", 0, "")
	e1, _ := c.Next(sampleEvent(1))
	c.Commit(e1)
	defer func() {
		if recover() == nil {
			t.Fatal("Commit ulang atas entri yang sudah ter-commit harus panik")
		}
	}()
	c.Commit(e1) // e1.Seq(1) != lastSeq(1)+1 → harus panik
}

func TestCanonicalJSONDeterministic(t *testing.T) {
	// Urutan penyisipan kunci berbeda → hash harus sama (kunci terurut).
	c1 := NewChain("n", 0, "")
	c2 := NewChain("n", 0, "")
	ts := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	e1, _ := c1.Append(Event{EventType: "X", CreatedAt: ts, Payload: map[string]any{"a": 1, "b": 2}})
	e2, _ := c2.Append(Event{EventType: "X", CreatedAt: ts, Payload: map[string]any{"b": 2, "a": 1}})
	if e1.CurrentHash != e2.CurrentHash {
		t.Fatal("canonical JSON harus deterministik terlepas urutan kunci")
	}
}
