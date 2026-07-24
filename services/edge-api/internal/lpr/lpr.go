// Package lpr — klien LPR/OCR sisi Edge (PRD §7). Abstraksi Recognizer memisahkan logika
// pipeline & penulisan ocr_logs dari transport konkret.
//
// Transport gRPC nyata ke lpr-svc (Python) ditunda: butuh stub protoc + model YOLOv8/EasyOCR
// (Fase 2, §17 item 3). Sesuai P2/§7.3, LPR BUKAN gerbang keputusan — bila layanan tak tersedia,
// hasil UNREAD dan kendaraan tetap dilayani. `Degraded` mengimplementasikan perilaku itu.
package lpr

import (
	"context"
	"time"
)

type Verdict string

const (
	VerdictAutoAccept  Verdict = "AUTO_ACCEPT"
	VerdictNeedsReview Verdict = "NEEDS_REVIEW"
	VerdictUnread      Verdict = "UNREAD"
)

// Result — hasil satu pembacaan, siap tulis ke ocr_logs (§7.2).
type Result struct {
	RawText         string
	NormalizedPlate string
	Confidence      float64
	VehicleType     string
	Verdict         Verdict
	LatencyMs       int
	EngineVersion   string
}

// Recognizer — kontrak klien LPR. Deadline dibawa ctx (NFR-1.1: ≤1000 ms).
type Recognizer interface {
	Recognize(ctx context.Context, image []byte, gateID string) (Result, error)
}

// DecideVerdict menentukan verdict dari confidence & validitas pola (§7.2).
func DecideVerdict(confidence float64, valid, timedOut bool) Verdict {
	if timedOut || confidence < 0.60 || !valid {
		return VerdictUnread
	}
	if confidence >= 0.85 {
		return VerdictAutoAccept
	}
	return VerdictNeedsReview
}

// Degraded — implementasi saat lpr-svc tidak tersedia. Selalu UNREAD (P2/§5.4.3).
type Degraded struct{ EngineVersion string }

func (d Degraded) Recognize(_ context.Context, _ []byte, _ string) (Result, error) {
	ev := d.EngineVersion
	if ev == "" {
		ev = "degraded"
	}
	return Result{Verdict: VerdictUnread, VehicleType: "mobil", EngineVersion: ev, LatencyMs: 0}, nil
}

// Stub — implementasi demo yang mengembalikan plat & confidence tetap (untuk latihan/UI tanpa model).
type Stub struct {
	Plate         string
	Confidence    float64
	VehicleType   string
	EngineVersion string
	Delay         time.Duration
}

func (s Stub) Recognize(ctx context.Context, _ []byte, _ string) (Result, error) {
	t0 := time.Now()
	if s.Delay > 0 {
		select {
		case <-time.After(s.Delay):
		case <-ctx.Done():
			return Result{Verdict: VerdictUnread, EngineVersion: s.EngineVersion}, nil // timeout → UNREAD
		}
	}
	vt := s.VehicleType
	if vt == "" {
		vt = "mobil"
	}
	valid := s.Plate != ""
	return Result{
		RawText: s.Plate, NormalizedPlate: s.Plate, Confidence: s.Confidence, VehicleType: vt,
		Verdict: DecideVerdict(s.Confidence, valid, false),
		LatencyMs: int(time.Since(t0).Milliseconds()), EngineVersion: s.EngineVersion,
	}, nil
}
