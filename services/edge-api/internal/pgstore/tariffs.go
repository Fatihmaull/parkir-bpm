package pgstore

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/jabar-creative/parkir/edge-api/internal/gate"
)

// ── gate.Tariffs ──

// Resolve mengambil tarif yang SEDANG berlaku (D5: tarif versioned, baris baru bukan UPDATE) —
// effective_from terbaru yang effective_to-nya belum lewat atau masih NULL (berlaku sampai
// sekarang).
//
// Tak menerima context (kontrak gate.Tariffs tak menyediakannya) — memakai context.Background()
// dengan sengaja; ini query baca cepat berindeks (site_id, vehicle_type, effective_from), bukan
// operasi yang butuh pembatalan.
func (s *Store) Resolve(vehicleType string) (gate.RateCard, bool) {
	var base int64
	var first sql.NullInt64
	err := s.pool.QueryRow(context.Background(), `
		SELECT base_rate, first_hour_rate FROM tariffs
		 WHERE site_id = $1 AND vehicle_type = $2
		   AND effective_from <= now() AND (effective_to IS NULL OR effective_to > now())
		 ORDER BY effective_from DESC LIMIT 1`,
		s.siteID, vehicleType,
	).Scan(&base, &first)
	if err != nil {
		slog.Warn("pgstore: tarif tak ditemukan", "vehicle_type", vehicleType, "err", err)
		return gate.RateCard{}, false
	}
	rc := gate.RateCard{BaseRate: base}
	if first.Valid {
		rc.FirstHourRate = first.Int64
	}
	return rc, true
}
