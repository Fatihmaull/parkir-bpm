package auditsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

func TestHTTPSinkPostsToAuditBatchEndpoint(t *testing.T) {
	var got batchReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path HARUS beda dari /internal/v1/sync/batch (syncagent) — jalur terpisah, task 8.5.
		if r.URL.Path != "/internal/v1/sync/audit-batch" {
			t.Errorf("path salah: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"applied":1,"skipped":0}`))
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, "t-jabar")
	items := []outbox.AuditItem{
		{ID: 1, NodeID: "node-a", Seq: 1, Payload: map[string]any{"node_id": "node-a", "seq": float64(1), "current_hash": "abc"}},
	}
	if err := sink.SendAuditBatch(context.Background(), items); err != nil {
		t.Fatalf("SendAuditBatch: %v", err)
	}
	if got.TenantID != "t-jabar" || len(got.Entries) != 1 || got.Entries[0]["node_id"] != "node-a" {
		t.Fatalf("payload batch tidak sesuai: %+v", got)
	}
}

func TestHTTPSinkErrorsOnServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	sink := NewHTTPSink(srv.URL, "t")
	if err := sink.SendAuditBatch(context.Background(), []outbox.AuditItem{{ID: 1, NodeID: "n", Seq: 1, Payload: map[string]any{}}}); err == nil {
		t.Fatal("harus error saat cloud 500 (agar item di-retry)")
	}
}
