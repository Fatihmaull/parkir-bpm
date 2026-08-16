package pgstore

import (
	"context"
	"log/slog"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/ids"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
)

// WriteOCR menyimpan satu baris ocr_logs (§7.2). Ditulis SELALU, sukses maupun gagal —
// kegagalan tulis di sini dicatat log, tak menjatuhkan alur gerbang (P2): hasil LPR tak
// pernah menggerbang keputusan gerbang, dan begitu pula log-nya tak boleh.
func (s *Store) WriteOCR(l memstore.OcrLog) {
	_, err := s.pool.Exec(context.Background(), `
		INSERT INTO ocr_logs (id, tenant_id, site_id, gate_id, captured_at, raw_text,
			normalized_plate, confidence, verdict, latency_ms, engine_version)
		VALUES ($1,$2,$3,NULL,$4,$5,$6,$7,$8,$9,$10)`,
		ids.NewV7(), s.tenantID, s.siteID, l.CapturedAt, nullIfEmpty(l.RawText),
		nullIfEmpty(l.NormalizedPlate), l.Confidence, l.Verdict, l.LatencyMs, l.EngineVersion)
	if err != nil {
		slog.Error("pgstore: WriteOCR gagal", "err", err)
	}
}

// OCRLogs mengembalikan N terbaru (lihat ocrWindowLimit) — tak menerima context (kontrak
// memstore.Store yang ditiru tak menyediakannya).
func (s *Store) OCRLogs() []memstore.OcrLog {
	rows, err := s.pool.Query(context.Background(), `
		SELECT captured_at, COALESCE(raw_text, ''), COALESCE(normalized_plate, ''), confidence,
		       verdict, latency_ms, engine_version
		  FROM ocr_logs WHERE site_id = $1 ORDER BY captured_at DESC LIMIT $2`,
		s.siteID, ocrWindowLimit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memstore.OcrLog
	for rows.Next() {
		var l memstore.OcrLog
		if err := rows.Scan(&l.CapturedAt, &l.RawText, &l.NormalizedPlate, &l.Confidence,
			&l.Verdict, &l.LatencyMs, &l.EngineVersion); err != nil {
			return out
		}
		out = append(out, l)
	}
	return out
}

// ActiveVehicles — kendaraan IN_PREMISES saat ini (untuk POS & metrik).
func (s *Store) ActiveVehicles() []memstore.ActiveVehicle {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, COALESCE(ticket_code, ''), vehicle_type, COALESCE(plate_in, ''), entry_time,
		       membership_id IS NOT NULL
		  FROM vehicles_log WHERE site_id = $1 AND status = 'IN_PREMISES'
		 ORDER BY entry_time`,
		s.siteID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memstore.ActiveVehicle
	for rows.Next() {
		var v memstore.ActiveVehicle
		if err := rows.Scan(&v.ID, &v.Ticket, &v.VehicleType, &v.Plate, &v.EntryTime, &v.IsMember); err != nil {
			return out
		}
		out = append(out, v)
	}
	return out
}

// TransactionViews mengembalikan seluruh vehicles_log bukan-DRAFT (§12.2/§12.3).
func (s *Store) TransactionViews() []memstore.TxnView {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, COALESCE(ticket_code, ''), vehicle_type, COALESCE(plate_in, ''), entry_time,
		       amount, status, flags
		  FROM vehicles_log WHERE site_id = $1 AND status <> 'DRAFT'
		 ORDER BY entry_time DESC`,
		s.siteID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memstore.TxnView
	for rows.Next() {
		var v memstore.TxnView
		if err := rows.Scan(&v.ID, &v.Ticket, &v.VehicleType, &v.PlateIn, &v.EntryTime,
			&v.Amount, &v.Status, &v.Flags); err != nil {
			return out
		}
		out = append(out, v)
	}
	return out
}

// PaymentViews mengembalikan seluruh payments site ini.
func (s *Store) PaymentViews() []memstore.PaymentView {
	rows, err := s.pool.Query(context.Background(), `
		SELECT method, amount, status, vehicles_log_id FROM payments WHERE site_id = $1
		 ORDER BY created_at DESC`,
		s.siteID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memstore.PaymentView
	for rows.Next() {
		var v memstore.PaymentView
		if err := rows.Scan(&v.Method, &v.Amount, &v.Status, &v.TxID); err != nil {
			return out
		}
		out = append(out, v)
	}
	return out
}

// MemberViews mengembalikan seluruh membership tenant ini.
func (s *Store) MemberViews() []memstore.MemberView {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, rfid_uid, plates, vehicle_type, valid_until, presence, status
		  FROM memberships WHERE tenant_id = $1 ORDER BY holder_name`,
		s.tenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memstore.MemberView
	for rows.Next() {
		var v memstore.MemberView
		var validUntil time.Time // kolom DATE
		if err := rows.Scan(&v.ID, &v.RfidUID, &v.Plates, &v.VehicleType, &validUntil,
			&v.Presence, &v.Status); err != nil {
			return out
		}
		v.ValidUntil = validUntil.Format("2006-01-02") // sama seperti memstore.MemberViews
		out = append(out, v)
	}
	return out
}

// OccupancyByType menghitung kendaraan IN_PREMISES per jenis (Mapping Slot §12.4).
func (s *Store) OccupancyByType() map[string]int {
	rows, err := s.pool.Query(context.Background(), `
		SELECT vehicle_type, count(*) FROM vehicles_log
		 WHERE site_id = $1 AND status = 'IN_PREMISES' GROUP BY vehicle_type`,
		s.siteID)
	if err != nil {
		return map[string]int{}
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var vt string
		var n int
		if err := rows.Scan(&vt, &n); err != nil {
			return out
		}
		out[vt] = n
	}
	return out
}
