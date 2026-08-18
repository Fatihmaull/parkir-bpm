package lpr

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jabar-creative/parkir/edge-api/internal/lpr/lprpb"
)

// GRPC — klien gRPC nyata ke lpr-svc (task 6.1), lewat stub yang di-generate dari
// proto/lpr.proto (protoc + protoc-gen-go + protoc-gen-go-grpc — lihat komentar file itu).
//
// Transportnya nyata; "kecerdasan" di baliknya (YOLOv8n/EasyOCR) masih placeholder di
// lpr-svc sampai task 6.2 — konsisten dengan pembagian scope 6.1 vs 6.2. Kegagalan RPC apa
// pun (server tak terjangkau, deadline lewat, dsb.) diteruskan sebagai error biasa: pemanggil
// (gatesvc.runLPR) SUDAH menurunkannya jadi UNREAD (P2/§7.3) — GRPC tak perlu menduplikasi
// logika degradasi itu di sini.
type GRPC struct {
	conn   *grpc.ClientConn
	client lprpb.LPRClient
}

// NewGRPC membuka koneksi ke lpr-svc di addr ("host:port"). grpc.NewClient tidak memblokir
// menunggu koneksi sungguhan terjadi (lazy connect) — cocok dengan P2: edge-api tak boleh
// menunggu lpr-svc hidup sebelum bisa melayani gerbang; kegagalan koneksi baru muncul saat
// RPC pertama dipanggil, dan itu pun cuma UNREAD, bukan gerbang berhenti.
func NewGRPC(addr string) (*GRPC, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("lpr: NewGRPC: %w", err)
	}
	return &GRPC{conn: conn, client: lprpb.NewLPRClient(conn)}, nil
}

// Close menutup koneksi. Aman dipanggil sekali saat edge-api berhenti.
func (g *GRPC) Close() error { return g.conn.Close() }

func (g *GRPC) Recognize(ctx context.Context, image []byte, gateID string) (Result, error) {
	resp, err := g.client.Recognize(ctx, &lprpb.RecognizeRequest{
		Image: image, GateId: gateID, CapturedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("lpr: Recognize: %w", err)
	}
	return Result{
		RawText: resp.RawText, NormalizedPlate: resp.NormalizedPlate,
		Confidence: resp.Confidence, VehicleType: resp.VehicleType,
		Verdict: Verdict(resp.Verdict), LatencyMs: int(resp.LatencyMs),
		EngineVersion: resp.EngineVersion,
	}, nil
}

var _ Recognizer = (*GRPC)(nil)
