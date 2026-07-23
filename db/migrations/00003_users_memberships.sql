-- PRD §11.2 — Pengguna & Member. RBAC PRD §12.0/§14, anti-passback PRD §8.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    email         TEXT NOT NULL,
    password_hash TEXT NOT NULL,          -- argon2id
    full_name     TEXT NOT NULL,
    role          TEXT NOT NULL,          -- SuperAdmin|Auditor|Kasir
    site_scope    UUID[] NOT NULL DEFAULT '{}',  -- kosong = semua site tenant
    status        TEXT NOT NULL DEFAULT 'active',
    last_login_at TIMESTAMPTZ,
    failed_logins INT NOT NULL DEFAULT 0,
    UNIQUE (tenant_id, email)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE memberships (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL REFERENCES tenants(id),
    rfid_uid       TEXT NOT NULL,
    holder_name    TEXT NOT NULL,
    unit_label     TEXT,
    plates         TEXT[] NOT NULL DEFAULT '{}',
    vehicle_type   TEXT NOT NULL,
    site_scope     UUID[] NOT NULL DEFAULT '{}',
    valid_from     DATE NOT NULL,
    valid_until    DATE NOT NULL,
    status         TEXT NOT NULL DEFAULT 'active',   -- active|expired|blocked
    presence       TEXT NOT NULL DEFAULT 'OUT',      -- IN|OUT (anti-passback)
    presence_since TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, rfid_uid)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_memberships_expiry ON memberships (valid_until)
    WHERE status = 'active';
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS users;
