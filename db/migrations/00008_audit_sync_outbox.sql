-- Task 8.5 — jalur sync TERPISAH untuk audit_logs, bukan dicampur ke sync_outbox generik
-- (vehicles_log/payments). Alasannya bukan sekadar rapi: kontinuitas rantai hash (§9.4)
-- menuntut SETIAP entri sampai ke Cloud berurutan tanpa celah — satu entri hilang/tertukar
-- urutan merusak verifikasi seluruh entri SESUDAHNYA, bukan cuma satu baris yang hilang
-- (beda dari vehicles_log, yang hilangnya satu baris tak mempengaruhi baris lain). Mencampur
-- ke antrean yang sama dengan data bisnis berarti kegagalan/backoff salah satu bisa
-- menahan yang lain, walau keduanya punya kebutuhan berbeda.
--
-- Dikunci oleh node_id, bukan aggregate_id — audit_logs tak punya "satu ID" yang berarti
-- bagi Cloud; yang berarti adalah urutan seq per node.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE audit_sync_outbox (
    id         BIGSERIAL PRIMARY KEY,
    node_id    TEXT NOT NULL,
    seq        BIGINT NOT NULL,
    payload    JSONB NOT NULL,
    status     TEXT NOT NULL DEFAULT 'PENDING',  -- PENDING|SENT|FAILED
    attempts   INT  NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at    TIMESTAMPTZ,
    UNIQUE (node_id, seq)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_audit_outbox_pending ON audit_sync_outbox (node_id, seq)
    WHERE status = 'PENDING';
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS audit_sync_outbox;
