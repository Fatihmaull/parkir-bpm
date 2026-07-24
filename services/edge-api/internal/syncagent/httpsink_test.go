package syncagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

func TestHTTPSinkPostsBatch(t *testing.T) {
	var got batchReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/sync/batch" {
			t.Errorf("path salah: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"applied":2,"skipped":0}`))
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, "t-jabar")
	items := []outbox.Item{
		{ID: 1, Aggregate: "vehicles_log", AggregateID: "v1", Payload: map[string]any{"amount": 1000}},
		{ID: 2, Aggregate: "vehicles_log", AggregateID: "v2", Payload: map[string]any{"amount": 2000}},
	}
	if err := sink.SendBatch(context.Background(), items); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	if got.TenantID != "t-jabar" || len(got.Items) != 2 || got.Items[0].AggregateID != "v1" {
		t.Fatalf("payload batch tidak sesuai: %+v", got)
	}
}

func TestHTTPSinkErrorsOnServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	sink := NewHTTPSink(srv.URL, "t")
	if err := sink.SendBatch(context.Background(), []outbox.Item{{ID: 1, AggregateID: "x"}}); err == nil {
		t.Fatal("harus error saat cloud 500 (agar item di-retry)")
	}
}

// End-to-end offline→online: batch gagal saat server down, lalu berhasil saat pulih,
// membuktikan nol kehilangan (NFR-2.2).
func TestSyncSurvivesOutageEndToEnd(t *testing.T) {
	up := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ob := outbox.NewMem()
	for i := 0; i < 3; i++ {
		ob.Enqueue("vehicles_log", "id"+string(rune('a'+i)), map[string]any{"n": i})
	}
	a := New(ob, NewHTTPSink(srv.URL, "t"), 200)

	_, _ = a.DrainOnce(context.Background()) // cloud down → gagal
	if ob.PendingCount() != 3 {
		t.Fatalf("saat cloud down semua harus tetap pending, got %d", ob.PendingCount())
	}
	up = true
	sent, err := a.DrainOnce(context.Background())
	if err != nil || sent != 3 || ob.PendingCount() != 0 {
		t.Fatalf("setelah pulih harus terkirim semua: sent=%d pending=%d err=%v", sent, ob.PendingCount(), err)
	}
}
