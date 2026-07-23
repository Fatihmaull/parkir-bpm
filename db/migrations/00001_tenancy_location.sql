-- PRD §11.2 — Tenancy & Lokasi. Multi-tenant sejak baris pertama (P6).
-- +goose Up
-- +goose StatementBegin
CREATE TABLE tenants (
    id           UUID PRIMARY KEY,
    code         TEXT UNIQUE NOT NULL,
    name         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sites (                      -- "Lahan Parkir"
    id           UUID PRIMARY KEY,
    tenant_id    UUID NOT NULL REFERENCES tenants(id),
    code         TEXT NOT NULL,           -- mis. "mall_jabar"
    name         TEXT NOT NULL,
    city         TEXT,
    address      TEXT,
    timezone     TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    grace_minutes            INT NOT NULL DEFAULT 15,
    peak_multiplier          NUMERIC(4,2) NOT NULL DEFAULT 1.00,
    peak_windows             JSONB NOT NULL DEFAULT '[]',
    max_daily_rate           BIGINT,
    lost_ticket_penalty      BIGINT NOT NULL DEFAULT 0,
    antipassback_reset_hours INT NOT NULL DEFAULT 18,
    cash_variance_threshold  BIGINT NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, code)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Tarif bersifat versioned: perubahan = baris baru, bukan UPDATE (D5).
CREATE TABLE tariffs (
    id           UUID PRIMARY KEY,
    site_id      UUID NOT NULL REFERENCES sites(id),
    vehicle_type TEXT NOT NULL,           -- mobil|motor|truk|bus
    base_rate    BIGINT NOT NULL,         -- rupiah utuh, per jam (D6: BIGINT, bukan float)
    first_hour_rate BIGINT,               -- NULL = sama dengan base_rate
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_to    TIMESTAMPTZ,          -- NULL = masih berlaku
    UNIQUE (site_id, vehicle_type, effective_from)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE slots_map (
    id            UUID PRIMARY KEY,
    site_id       UUID NOT NULL REFERENCES sites(id),
    vehicle_type  TEXT NOT NULL,
    capacity      INT  NOT NULL,
    zone          TEXT,
    UNIQUE (site_id, vehicle_type, zone)
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS slots_map;
DROP TABLE IF EXISTS tariffs;
DROP TABLE IF EXISTS sites;
DROP TABLE IF EXISTS tenants;
