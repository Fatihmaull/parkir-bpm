package syncagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// HTTPSink mengirim batch ke cloud-api POST /internal/v1/sync/batch (PRD §10.2/§13.2).
// Produksi: lewat Cloudflare Tunnel + mTLS per node (FR-1.4). Di sini HTTP biasa.
type HTTPSink struct {
	Endpoint string // mis. https://cloud.example.com
	TenantID string
	Client   *http.Client
}

func NewHTTPSink(endpoint, tenantID string) *HTTPSink {
	return &HTTPSink{
		Endpoint: endpoint, TenantID: tenantID,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

type batchItem struct {
	AggregateID string         `json:"aggregate_id"`
	Aggregate   string         `json:"aggregate"`
	Payload     map[string]any `json:"payload"`
}

type batchReq struct {
	TenantID string      `json:"tenant_id"`
	Items    []batchItem `json:"items"`
}

func (s *HTTPSink) SendBatch(ctx context.Context, items []outbox.Item) error {
	body := batchReq{TenantID: s.TenantID}
	for _, it := range items {
		body.Items = append(body.Items, batchItem{
			AggregateID: it.AggregateID, Aggregate: it.Aggregate, Payload: it.Payload,
		})
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.Endpoint+"/internal/v1/sync/batch", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err // Cloud tak terjangkau → item tetap PENDING, di-retry (offline-first P1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sync: cloud membalas %d", resp.StatusCode)
	}
	return nil
}
