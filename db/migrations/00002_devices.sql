-- PRD §11.2 — Perangkat (gerbang & device). Peta port PRD §5.1.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE gates (
    id              UUID PRIMARY KEY,
    site_id         UUID NOT NULL REFERENCES sites(id),
    code            TEXT NOT NULL,          -- "GATE-IN-01"
    kind            TEXT NOT NULL,          -- ENTRY|EXIT
    controller_addr SMALLINT NOT NULL,      -- 0x01 / 0x02
    transport       TEXT NOT NULL,          -- serial|tcp|sim
    endpoint        TEXT NOT NULL,          -- "COM3" | "10.0.0.5:5001"
    config          JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'active',
    UNIQUE (site_id, code)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE devices (
    id            UUID PRIMARY KEY,
    gate_id       UUID REFERENCES gates(id),
    site_id       UUID NOT NULL REFERENCES sites(id),
    kind          TEXT NOT NULL,   -- BARRIER|LOOP|PRINTER|RFID|CAMERA|EDC|LIGHT
    label         TEXT NOT NULL,   -- "LD1", "Printer Masuk"
    address       TEXT,
    config        JSONB NOT NULL DEFAULT '{}',
    last_seen_at  TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'unknown'  -- online|offline|fault|unknown
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS gates;
