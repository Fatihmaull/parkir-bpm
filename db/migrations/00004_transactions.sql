-- PRD §11.2 — Transaksi: vehicles_log, payments, ocr_logs, shifts.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE vehicles_log (
    id            UUID PRIMARY KEY,                  -- UUIDv7 (dibuat di Edge)
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    site_id       UUID NOT NULL REFERENCES sites(id),
    ticket_code   TEXT,
    vehicle_type  TEXT NOT NULL,
    membership_id UUID REFERENCES memberships(id),
    plate_in      TEXT,
    plate_out     TEXT,
    img_in_key    TEXT,
    img_out_key   TEXT,
    entry_time    TIMESTAMPTZ NOT NULL,
    exit_time     TIMESTAMPTZ,
    entry_gate_id UUID REFERENCES gates(id),
    exit_gate_id  UUID REFERENCES gates(id),
    duration_min  INT,
    amount        BIGINT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL,     -- IN_PREMISES|COMPLETED|VOID|DISPUTE
    flags         TEXT[] NOT NULL DEFAULT '{}',
                  -- needs_review | no_show | lost_ticket | unregistered_entry
                  -- | photo_mismatch | manual_override
    operator_id   UUID REFERENCES users(id),
    shift_id      UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_vl_ticket ON vehicles_log (site_id, ticket_code)
    WHERE ticket_code IS NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_vl_active ON vehicles_log (site_id, status)
    WHERE status = 'IN_PREMISES';
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_vl_entry ON vehicles_log (site_id, entry_time DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_vl_plate ON vehicles_log (site_id, plate_in);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE payments (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    site_id         UUID NOT NULL,
    vehicles_log_id UUID NOT NULL REFERENCES vehicles_log(id),
    shift_id        UUID,
    method          TEXT NOT NULL,   -- CASH|EDC_EMONEY|EDC_DEBIT|QRIS|EWALLET|MEMBER
    amount          BIGINT NOT NULL,
    tendered        BIGINT,          -- untuk CASH
    change_given    BIGINT,
    status          TEXT NOT NULL,   -- PENDING|SETTLED|FAILED|REFUNDED
    provider        TEXT,            -- midtrans|xendit|edc_vendor
    provider_ref    TEXT,            -- order_id / trace no
    card_type       TEXT,            -- FLAZZ|EMONEY|BRIZZI|TAPCASH|DEBIT
    masked_pan      TEXT,            -- HANYA masked (PRD §6.2.3 aturan #3)
    approval_code   TEXT,
    terminal_id     TEXT,
    batch_no        TEXT,
    balance_after   BIGINT,
    raw_response    JSONB,
    settled_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
-- Idempotensi webhook PG (PRD §6.3): provider_ref unik per provider.
CREATE UNIQUE INDEX idx_pay_provider_ref ON payments (provider, provider_ref)
    WHERE provider_ref IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE ocr_logs (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    site_id         UUID NOT NULL,
    vehicles_log_id UUID REFERENCES vehicles_log(id),
    gate_id         UUID REFERENCES gates(id),
    captured_at     TIMESTAMPTZ NOT NULL,
    raw_text        TEXT,
    normalized_plate TEXT,
    confidence      NUMERIC(4,3),
    verdict         TEXT NOT NULL,   -- AUTO_ACCEPT|NEEDS_REVIEW|UNREAD
    corrected_plate TEXT,
    corrected_by    UUID REFERENCES users(id),
    corrected_at    TIMESTAMPTZ,
    latency_ms      INT,
    engine_version  TEXT,
    image_key       TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_ocr_review ON ocr_logs (site_id, captured_at DESC)
    WHERE verdict <> 'AUTO_ACCEPT';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE shifts (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    site_id       UUID NOT NULL,
    operator_id   UUID NOT NULL REFERENCES users(id),
    opened_at     TIMESTAMPTZ NOT NULL,
    closed_at     TIMESTAMPTZ,
    opening_float BIGINT NOT NULL DEFAULT 0,
    declared_cash BIGINT,
    system_cash   BIGINT,
    system_edc    BIGINT,
    system_qris   BIGINT,
    variance      BIGINT,
    note          TEXT,
    status        TEXT NOT NULL DEFAULT 'OPEN'   -- OPEN|CLOSED|VARIANCE
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS shifts;
DROP TABLE IF EXISTS ocr_logs;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS vehicles_log;
