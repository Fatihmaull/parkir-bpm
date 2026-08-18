package pgstore

import (
	"context"
	"fmt"

	"github.com/jabar-creative/parkir/edge-api/internal/gatesvc"
)

// LoadGates memenuhi gatesvc.GateSource (task 2.1) — daftar gerbang datang dari tabel
// `gates`, bukan `.env`. Hanya gerbang berstatus `active` yang dimuat; nonaktifkan gerbang
// lewat `UPDATE gates SET status='inactive'`, bukan `DELETE` — device/audit trail yang
// merujuk `gate_id`-nya (mis. `devices.gate_id`, event `gate_code`) tetap harus bermakna.
//
// Urutan ORDER BY code menjaga daftar & log stabil lintas restart — sama seperti
// StaticSource, yang urutannya memang tetap karena berasal dari slice yang sama tiap kali.
func (s *Store) LoadGates(ctx context.Context) ([]gatesvc.GateSpec, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT code, kind, transport, endpoint FROM gates
		 WHERE site_id = $1 AND status = 'active'
		 ORDER BY code`,
		s.siteID)
	if err != nil {
		return nil, fmt.Errorf("pgstore: LoadGates: %w", err)
	}
	defer rows.Close()

	var out []gatesvc.GateSpec
	for rows.Next() {
		var spec gatesvc.GateSpec
		var kind string
		if err := rows.Scan(&spec.Code, &kind, &spec.Transport, &spec.Endpoint); err != nil {
			return nil, fmt.Errorf("pgstore: LoadGates scan: %w", err)
		}
		spec.Kind = gatesvc.GateKind(kind)
		out = append(out, spec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgstore: LoadGates: %w", err)
	}
	// Daftar gerbang kosong dari sumber pgx lebih baik dianggap kesalahan konfigurasi
	// (site belum di-seed gerbangnya) daripada diam-diam jatuh ke default 2-gerbang-sim
	// milik DefaultSpecs — itu akan menyamarkan "lupa seed" sebagai "lahan demo sengaja".
	if len(out) == 0 {
		return nil, fmt.Errorf("pgstore: tak ada gerbang berstatus active untuk site_id %s"+
			" — isi tabel gates (lihat db/seed/dev_seed.sql) sebelum EDGE_STORE=postgres", s.siteID)
	}
	return out, nil
}

var _ gatesvc.GateSource = (*Store)(nil)
