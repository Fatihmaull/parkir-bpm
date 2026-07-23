-- PRD §9 & §11.2 — Audit log append-only (rantai hash per node), outbox, alerts.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE audit_logs (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    site_id       UUID,
    node_id       TEXT NOT NULL,
    seq           BIGINT NOT NULL,
    event_type    TEXT NOT NULL,
    severity      TEXT NOT NULL,   -- normal|warning|critical
    actor_id      UUID,
    actor_label   TEXT NOT NULL,
    actor_role    TEXT NOT NULL,   -- SuperAdmin|Auditor|Kasir|System
    gate_label    TEXT,
    device_label  TEXT,
    summary       TEXT NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}',
    previous_hash CHAR(64) NOT NULL,
    current_hash  CHAR(64) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (node_id, seq)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_audit_sev ON audit_logs (tenant_id, severity, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Immutability di level DB (PRD §9.2) — trigger, bukan sekadar disiplin kode.
CREATE OR REPLACE FUNCTION audit_logs_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs bersifat append-only: operasi % ditolak', TG_OP;
END; $$ LANGUAGE plpgsql;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER trg_audit_no_update BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();
-- +goose StatementEnd
-- Catatan: REVOKE UPDATE/DELETE/TRUNCATE ... FROM app_user dijalankan terpisah
-- setelah role aplikasi dibuat (lihat db/grants.sql).

-- +goose StatementBegin
CREATE TABLE sync_outbox (
    id           BIGSERIAL PRIMARY KEY,
    aggregate    TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    payload      JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'PENDING',  -- PENDING|SENT|FAILED
    attempts     INT  NOT NULL DEFAULT 0,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at      TIMESTAMPTZ
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_outbox_pending ON sync_outbox (created_at)
    WHERE status = 'PENDING';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE alerts (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    site_id         UUID,
    type            TEXT NOT NULL,
    severity        TEXT NOT NULL,
    message         TEXT NOT NULL,
    device_id       UUID REFERENCES devices(id),
    context         JSONB NOT NULL DEFAULT '{}',
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_by UUID REFERENCES users(id),
    acknowledged_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_audit_no_update ON audit_logs;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS audit_logs_immutable();
-- +goose StatementEnd
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS sync_outbox;
DROP TABLE IF EXISTS audit_logs;
