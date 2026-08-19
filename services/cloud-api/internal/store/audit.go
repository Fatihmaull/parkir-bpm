// Penerima sync audit_logs dari Edge (task 8.5) — jalur TERPISAH dari ApplyBatch/Txn
// (store.go). Cloud tidak percaya begitu saja apa yang dikirim: setiap entri di-verifikasi
// ulang secara kriptografis (rantai hash per node, §9.1/§9.4) sebelum diterima, bukan hanya
// disimpan mentah. Formula hash SENGAJA diduplikasi dari internal/audit (edge-api) alih-alih
// diimpor — cloud-api & edge-api adalah modul Go terpisah (batas layanan §4.2), dan duplikasi
// kecil ini terdokumentasi di sini daripada memaksa coupling lintas-service demi satu fungsi.
// Kalau formula berubah di satu sisi, ia HARUS diubah di sisi lain juga — lihat
// docs/CATATAN_KEPUTUSAN.md.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// GenesisHash — sama persis dengan audit.GenesisHash di edge-api (harus identik: previous_hash
// entri seq=1 dibandingkan dengan ini).
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditEntry — satu baris audit_logs yang sudah masuk & lolos verifikasi rantai di Cloud.
type AuditEntry struct {
	TenantID     string         `json:"tenant_id"`
	SiteID       string         `json:"site_id"`
	NodeID       string         `json:"node_id"`
	Seq          int64          `json:"seq"`
	EventType    string         `json:"event_type"`
	Severity     string         `json:"severity"`
	ActorID      string         `json:"actor_id"`
	ActorLabel   string         `json:"actor_label"`
	ActorRole    string         `json:"actor_role"`
	GateLabel    string         `json:"gate_label"`
	Summary      string         `json:"summary"`
	Payload      map[string]any `json:"payload"`
	PreviousHash string         `json:"previous_hash"`
	CurrentHash  string         `json:"current_hash"`
	CreatedAt    time.Time      `json:"created_at"`
	ReceivedAt   time.Time      `json:"received_at"`
}

// auditChainState — checkpoint rantai per node: cukup untuk memvalidasi entri BERIKUTNYA
// tanpa scan ulang seluruh histori pada tiap batch (beda dengan VerifyAuditChain penuh, yang
// memang sengaja scan ulang untuk audit on-demand §9.4).
type auditChainState struct {
	tenantID string
	siteID   string
	lastSeq  int64
	lastHash string
}

// auditStore — state task 8.5, dipisah dari field Store utama (store.go) supaya jelas ini
// concern audit-chain, dikunci dengan mutex sendiri (rantai tiap node independen dari
// txns/orders, tak ada alasan berbagi lock).
type auditStore struct {
	mu      sync.RWMutex
	chains  map[string]*auditChainState // key: node_id
	entries map[string][]AuditEntry     // key: node_id, terurut naik berdasarkan seq
}

func newAuditStore() *auditStore {
	return &auditStore{chains: make(map[string]*auditChainState), entries: make(map[string][]AuditEntry)}
}

// computeHash — identik dengan audit.computeHash di edge-api (lihat komentar paket).
func computeHash(prevHash, nodeID string, seq int64, eventType, actorID string, payload map[string]any, createdAt time.Time) (string, error) {
	payloadJSON, err := canonicalJSON(payload)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x1f%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s",
		prevHash, nodeID, seq, eventType, actorID, payloadJSON, createdAt.Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func canonicalJSON(payload map[string]any) (string, error) {
	if payload == nil {
		return "{}", nil
	}
	b, err := json.Marshal(payload) // encoding/json urutkan kunci map alfabetis → deterministik
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// AuditBatchResult — hasil satu panggilan ApplyAuditBatch, dikembalikan lengkap supaya endpoint
// HTTP bisa melaporkan status per node (bukan cuma angka agregat) ke operator/dashboard.
type AuditBatchResult struct {
	Applied int      `json:"applied"`
	Skipped int      `json:"skipped"`          // duplikat idempoten (retry aman, sudah pernah masuk)
	Broken  []string `json:"broken,omitempty"` // "node_id: pesan" — entri DITOLAK, celah/hash tak cocok
}

// ApplyAuditBatch menerima batch dari auditsync.HTTPSink (edge-api, task 8.5). Setiap entri
// diverifikasi terhadap checkpoint rantai node-nya SEBELUM disimpan:
//   - seq harus persis lastSeq+1 (kontinuitas — celah ditolak, TIDAK diam-diam dilompati)
//   - previous_hash harus sama dengan lastHash tersimpan
//   - current_hash dihitung ULANG dari isi entri & harus cocok (integritas isi)
//
// seq <= lastSeq berarti pengiriman ulang (retry setelah gagal sebelum ACK) → dilewati sebagai
// applied semu (idempoten, P1), BUKAN error, asalkan hash entri lama & baru sama persis; kalau
// beda berarti entri sudah berubah di antara pengiriman — itu ditolak sebagai potensi manipulasi.
// Entri dengan seq > lastSeq+1 (lompatan) DITOLAK dan TIDAK menggerakkan checkpoint — auditsync
// di Edge akan mengirim ulang tanpa batas (outbox.AuditPG.MarkAuditFailed tak pernah menyerah),
// jadi celah akan tertutup begitu entri yang hilang tiba, bukan dilewati permanen.
func (s *Store) ApplyAuditBatch(tenantID string, raw []map[string]any) AuditBatchResult {
	s.audit.mu.Lock()
	defer s.audit.mu.Unlock()

	// Kelompokkan per node lalu urutkan per seq — batch bisa berisi beberapa node sekaligus
	// (multi-gerbang, §5) dan urutan pengiriman JSON tak dijamin menyamai urutan seq.
	byNode := map[string][]AuditEntry{}
	for _, m := range raw {
		e, err := parseAuditEntry(tenantID, m)
		if err != nil {
			continue // payload cacat — dihitung skip-tanpa-detail, tak boleh membuat handler panik
		}
		byNode[e.NodeID] = append(byNode[e.NodeID], e)
	}

	var res AuditBatchResult
	for nodeID, list := range byNode {
		sort.Slice(list, func(i, j int) bool { return list[i].Seq < list[j].Seq })

		st, ok := s.audit.chains[nodeID]
		if !ok {
			st = &auditChainState{tenantID: tenantID, siteID: list[0].SiteID, lastSeq: 0, lastHash: GenesisHash}
			s.audit.chains[nodeID] = st
		}

		for _, e := range list {
			switch {
			case e.Seq <= st.lastSeq:
				// Retry/duplikat — verifikasi entri lama masih sama sebelum melewatinya diam-diam.
				if prior := findEntry(s.audit.entries[nodeID], e.Seq); prior != nil && prior.CurrentHash == e.CurrentHash {
					res.Skipped++
				} else {
					res.Broken = append(res.Broken, fmt.Sprintf("%s: seq %d retry tak cocok dengan entri tersimpan", nodeID, e.Seq))
				}
			case e.Seq != st.lastSeq+1:
				res.Broken = append(res.Broken, fmt.Sprintf("%s: celah rantai — diharapkan seq %d, diterima %d", nodeID, st.lastSeq+1, e.Seq))
			case e.PreviousHash != st.lastHash:
				res.Broken = append(res.Broken, fmt.Sprintf("%s: previous_hash seq %d tak menyambung ke checkpoint", nodeID, e.Seq))
			default:
				want, err := computeHash(e.PreviousHash, e.NodeID, e.Seq, e.EventType, e.ActorID, e.Payload, e.CreatedAt)
				if err != nil || want != e.CurrentHash {
					res.Broken = append(res.Broken, fmt.Sprintf("%s: current_hash seq %d tak cocok — isi termodifikasi?", nodeID, e.Seq))
					continue
				}
				e.ReceivedAt = time.Now()
				s.audit.entries[nodeID] = append(s.audit.entries[nodeID], e)
				st.lastSeq = e.Seq
				st.lastHash = e.CurrentHash
				res.Applied++
			}
		}
	}
	return res
}

func findEntry(list []AuditEntry, seq int64) *AuditEntry {
	for i := range list {
		if list[i].Seq == seq {
			return &list[i]
		}
	}
	return nil
}

func parseAuditEntry(tenantID string, m map[string]any) (AuditEntry, error) {
	var e AuditEntry
	e.TenantID = tenantID
	e.SiteID, _ = m["site_id"].(string)
	e.NodeID, _ = m["node_id"].(string)
	e.EventType, _ = m["event_type"].(string)
	e.Severity, _ = m["severity"].(string)
	e.ActorID, _ = m["actor_id"].(string)
	e.ActorLabel, _ = m["actor_label"].(string)
	e.ActorRole, _ = m["actor_role"].(string)
	e.GateLabel, _ = m["gate_label"].(string)
	e.Summary, _ = m["summary"].(string)
	e.PreviousHash, _ = m["previous_hash"].(string)
	e.CurrentHash, _ = m["current_hash"].(string)
	if p, ok := m["payload"].(map[string]any); ok {
		e.Payload = p
	}
	seqF, ok := m["seq"].(float64) // JSON angka selalu float64 lewat map[string]any
	if !ok || e.NodeID == "" || e.CurrentHash == "" {
		return e, fmt.Errorf("entri audit tak lengkap")
	}
	e.Seq = int64(seqF)
	createdStr, _ := m["created_at"].(string)
	ts, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return e, fmt.Errorf("created_at tak valid: %w", err)
	}
	e.CreatedAt = ts
	return e, nil
}

// AuditChainSummary — status kontinuitas satu rantai node, untuk GET /api/v1/audit.
type AuditChainSummary struct {
	NodeID     string    `json:"node_id"`
	SiteID     string    `json:"site_id"`
	LastSeq    int64     `json:"last_seq"`
	EntryCount int       `json:"entry_count"`
	LastHash   string    `json:"last_hash"`
	ReceivedAt time.Time `json:"last_received_at"`
}

// AuditChains mengembalikan ringkasan tiap rantai node milik tenantID (§12.14 — ter-scope).
func (s *Store) AuditChains(tenantID string) []AuditChainSummary {
	s.audit.mu.RLock()
	defer s.audit.mu.RUnlock()
	var out []AuditChainSummary
	for nodeID, st := range s.audit.chains {
		if st.tenantID != tenantID {
			continue
		}
		list := s.audit.entries[nodeID]
		sum := AuditChainSummary{NodeID: nodeID, SiteID: st.siteID, LastSeq: st.lastSeq, EntryCount: len(list), LastHash: st.lastHash}
		if len(list) > 0 {
			sum.ReceivedAt = list[len(list)-1].ReceivedAt
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// AuditEntries mengembalikan seluruh entri tersimpan untuk satu node (dibatasi tenantID).
// Dipakai ekspor/inspeksi (§9.4) — bukan endpoint volume tinggi, jadi scan penuh cukup (sama
// pertimbangan seperti pgstore.AuditEntries di edge-api).
func (s *Store) AuditEntries(tenantID, nodeID string) []AuditEntry {
	s.audit.mu.RLock()
	defer s.audit.mu.RUnlock()
	st, ok := s.audit.chains[nodeID]
	if !ok || st.tenantID != tenantID {
		return nil
	}
	out := make([]AuditEntry, len(s.audit.entries[nodeID]))
	copy(out, s.audit.entries[nodeID])
	return out
}

// VerifyAuditChain memverifikasi ULANG seluruh rantai satu node dari genesis (§9.4, on-demand —
// beda dari checkpoint incremental yang dipakai ApplyAuditBatch tiap sync). Mengembalikan seq
// pertama yang rusak (0 = utuh) dan jumlah entri yang diperiksa.
func (s *Store) VerifyAuditChain(tenantID, nodeID string) (brokenSeq int64, ok bool, checked int) {
	s.audit.mu.RLock()
	list := make([]AuditEntry, len(s.audit.entries[nodeID]))
	copy(list, s.audit.entries[nodeID])
	tid := ""
	if st, found := s.audit.chains[nodeID]; found {
		tid = st.tenantID
	}
	s.audit.mu.RUnlock()

	if tid != tenantID {
		return 0, true, 0 // node tak dikenal/tenant lain — tak ada yang diverifikasi, bukan "rusak"
	}

	prev := GenesisHash
	var expectedSeq int64 = 1
	for _, e := range list {
		checked++
		if e.Seq != expectedSeq || e.PreviousHash != prev {
			return e.Seq, false, checked
		}
		want, err := computeHash(e.PreviousHash, e.NodeID, e.Seq, e.EventType, e.ActorID, e.Payload, e.CreatedAt)
		if err != nil || want != e.CurrentHash {
			return e.Seq, false, checked
		}
		prev = e.CurrentHash
		expectedSeq++
	}
	return 0, true, checked
}
