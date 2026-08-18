package pgstore

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ── gate.TicketGen ──

// Next menaikkan ticket_counters lewat UPDATE ... RETURNING (row lock implisit PostgreSQL) —
// aman dari nomor tiket kembar saat beberapa kasir/gerbang menekan tombol nyaris bersamaan,
// tanpa perlu SELECT ... FOR UPDATE terpisah. Kontrak gate.TicketGen tak menyediakan context
// maupun jalur error (meniru memstore); kalau UPDATE gagal (pool down dsb.), gerbang tak
// boleh berhenti mengeluarkan tiket hanya karena penomoran macet (P2) — fallback ke nomor
// berbasis waktu yang tetap unik, dicatat sebagai Warning lewat log.
func (s *Store) Next() (string, string) {
	var n int64
	err := s.pool.QueryRow(context.Background(),
		`UPDATE ticket_counters SET counter = counter + 1 WHERE site_id = $1 RETURNING counter`,
		s.siteID,
	).Scan(&n)
	if err != nil {
		slog.Warn("pgstore: ticket_counters gagal dinaikkan, pakai fallback berbasis waktu", "err", err)
		n = time.Now().UnixNano()
	}
	code := fmt.Sprintf("TKT-%06d", n)
	return code, "PARKIR:" + code
}
