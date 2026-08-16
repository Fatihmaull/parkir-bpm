// Package audit — rantai hash immutable per node (PRD §9.1). Append-only (P5).
//
//	current_hash = SHA256(previous_hash ‖ node_id ‖ seq ‖ event_type ‖ actor_id ‖
//	                      canonical_json(payload) ‖ created_at_rfc3339nano)
//
// Rantai per (tenant_id, node_id), bukan global — tiap Edge menulis rantainya sendiri
// secara independen agar tetap jalan saat offline. Cloud memverifikasi tiap rantai terpisah.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// GenesisHash — previous_hash untuk baris genesis (seq 0) per node.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

type Severity string

const (
	SevNormal   Severity = "normal"
	SevWarning  Severity = "warning"
	SevCritical Severity = "critical"
)

// Event — masukan untuk menambah entri ke rantai.
type Event struct {
	TenantID   string
	SiteID     string
	EventType  string
	Severity   Severity
	ActorID    string // "" untuk aktor System
	ActorLabel string
	ActorRole  string // SuperAdmin|Auditor|Kasir|System
	GateLabel  string
	Summary    string
	Payload    map[string]any
	CreatedAt  time.Time
}

// Entry — entri rantai yang sudah ter-hash, siap disimpan ke audit_logs.
type Entry struct {
	NodeID       string
	Seq          int64
	Event        Event
	PreviousHash string
	CurrentHash  string
}

// Chain menyusun entri berurutan untuk satu node.
type Chain struct {
	nodeID   string
	lastSeq  int64
	lastHash string
}

// NewChain memulai (atau melanjutkan) rantai. Untuk node baru: lastSeq=0, lastHash="".
// Untuk melanjutkan dari DB, berikan seq & hash entri terakhir yang tersimpan.
func NewChain(nodeID string, lastSeq int64, lastHash string) *Chain {
	if lastHash == "" {
		lastHash = GenesisHash
	}
	return &Chain{nodeID: nodeID, lastSeq: lastSeq, lastHash: lastHash}
}

// Append menghitung entri berikutnya (seq+1) dan memajukan state rantai. Setara Next+Commit
// sekaligus — cocok untuk memstore yang tak pernah gagal menyimpan (in-memory).
func (c *Chain) Append(e Event) (Entry, error) {
	entry, err := c.Next(e)
	if err != nil {
		return Entry{}, err
	}
	c.Commit(entry)
	return entry, nil
}

// Next menghitung entri berikutnya TANPA memajukan state rantai (P5: append-only harus
// pasti tersimpan sebelum dianggap terjadi). Dipakai repository pgx (task 5.1): hitung dulu,
// INSERT ke audit_logs, baru Commit bila INSERT sukses. Kalau Next dipanggil lagi tanpa Commit
// sebelumnya (mis. INSERT gagal), ia menghitung ulang seq yang sama — aman untuk dicoba lagi.
func (c *Chain) Next(e Event) (Entry, error) {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	seq := c.lastSeq + 1
	h, err := computeHash(c.lastHash, c.nodeID, seq, e)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		NodeID:       c.nodeID,
		Seq:          seq,
		Event:        e,
		PreviousHash: c.lastHash,
		CurrentHash:  h,
	}, nil
}

// Commit memajukan state rantai setelah entry (dari Next) berhasil disimpan secara durable.
// Panik bila entry bukan lanjutan langsung dari state saat ini — itu bug pemanggil (commit di
// luar urutan), bukan sesuatu yang aman untuk "diperbaiki" diam-diam.
func (c *Chain) Commit(entry Entry) {
	if entry.NodeID != c.nodeID || entry.Seq != c.lastSeq+1 || entry.PreviousHash != c.lastHash {
		panic(fmt.Sprintf("audit: Commit di luar urutan: entry seq=%d prev=%s, chain lastSeq=%d lastHash=%s",
			entry.Seq, entry.PreviousHash, c.lastSeq, c.lastHash))
	}
	c.lastSeq = entry.Seq
	c.lastHash = entry.CurrentHash
}

// LastHash mengembalikan hash terakhir (untuk disimpan/di-sync ke Cloud).
func (c *Chain) LastHash() string { return c.lastHash }

// computeHash menghitung current_hash sesuai formula §9.1.
func computeHash(prevHash, nodeID string, seq int64, e Event) (string, error) {
	payloadJSON, err := canonicalJSON(e.Payload)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	// Pemisah "\x1f" (unit separator) mencegah ambiguitas penggabungan field.
	fmt.Fprintf(h, "%s\x1f%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s",
		prevHash, nodeID, seq, e.EventType, e.ActorID, payloadJSON,
		e.CreatedAt.Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canonicalJSON menghasilkan JSON deterministik (kunci terurut, tanpa whitespace).
// encoding/json me-marshal map dengan kunci terurut alfabetis → hash deterministik lintas bahasa.
func canonicalJSON(payload map[string]any) (string, error) {
	if payload == nil {
		return "{}", nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verify memeriksa kontinuitas & integritas rantai penuh (PRD §9.4).
// Mengembalikan seq pertama yang rusak (>0) atau 0 bila utuh.
func Verify(entries []Entry) (brokenSeq int64, ok bool) {
	prev := GenesisHash
	var expectedSeq int64 = 1
	for _, e := range entries {
		if e.Seq != expectedSeq {
			return e.Seq, false // lompatan/duplikasi seq
		}
		if e.PreviousHash != prev {
			return e.Seq, false // previous_hash tidak menyambung
		}
		want, err := computeHash(e.PreviousHash, e.NodeID, e.Seq, e.Event)
		if err != nil || want != e.CurrentHash {
			return e.Seq, false // isi termodifikasi
		}
		prev = e.CurrentHash
		expectedSeq++
	}
	return 0, true
}
