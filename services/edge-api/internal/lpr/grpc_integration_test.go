//go:build integration

// Uji klien gRPC (task 6.1) terhadap lpr-svc SUNGGUHAN (proses Python nyata, bukan mock) —
// sengaja lewat build tag integration yang sama dengan pgstore: butuh proses lain hidup,
// bukan sesuatu yang aman dijalankan tanpa syarat di `go test` biasa.
//
//	python -m lpr_svc.server &  # dari services/lpr-svc, dengan LPR_GRPC_ADDR diset
//	LPR_GRPC_ADDR=... go test -tags=integration ./internal/lpr/...
package lpr

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGRPCRecognizeAgainstRealServer(t *testing.T) {
	addr := os.Getenv("LPR_GRPC_ADDR")
	if addr == "" {
		t.Skip("LPR_GRPC_ADDR kosong — uji integrasi klien gRPC dilewati (lihat komentar berkas)")
	}

	client, err := NewGRPC(addr)
	if err != nil {
		t.Fatalf("NewGRPC: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := client.Recognize(ctx, []byte("frame-palsu"), "GATE-IN-01")
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	// lpr-svc masih placeholder (task 6.2 belum jalan) — yang dibuktikan di sini adalah
	// TRANSPORT gRPC-nya nyata dan berfungsi, bukan akurasi model yang belum ada.
	if res.Verdict != VerdictUnread {
		t.Fatalf("lpr-svc placeholder harus selalu UNREAD (confidence 0), dapat %+v", res)
	}
	if res.EngineVersion == "" {
		t.Fatalf("engine_version kosong — respons tak benar-benar dari server: %+v", res)
	}
}
