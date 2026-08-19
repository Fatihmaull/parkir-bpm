package auditsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/outbox"
)

// HTTPSink mengirim batch audit ke cloud-api POST /internal/v1/sync/audit-batch — endpoint
// TERPISAH dari /internal/v1/sync/batch (task 8.5). Produksi: lewat Cloudflare Tunnel +
// mTLS per node (FR-1.4), sama seperti syncagent.HTTPSink.
type HTTPSink struct {
	Endpoint string
	TenantID string
	Client   *http.Client
}

func NewHTTPSink(endpoint, tenantID string) *HTTPSink {
	return &HTTPSink{
		Endpoint: endpoint, TenantID: tenantID,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

type batchReq struct {
	TenantID string           `json:"tenant_id"`
	Entries  []map[string]any `json:"entries"`
}

func (s *HTTPSink) SendAuditBatch(ctx context.Context, items []outbox.AuditItem) error {
	body := batchReq{TenantID: s.TenantID}
	for _, it := range items {
		body.Entries = append(body.Entries, it.Payload)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.Endpoint+"/internal/v1/sync/audit-batch", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err // Cloud tak terjangkau → item tetap belum-terkirim, di-retry (P1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("audit sync: cloud membalas %d", resp.StatusCode)
	}
	return nil
}
