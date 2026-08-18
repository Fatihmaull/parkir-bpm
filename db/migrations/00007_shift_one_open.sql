-- Task 7.4 — satu shift AKTIF per site ditegakkan di level DB, bukan cek
-- SELECT-lalu-INSERT di kode (yang punya celah balapan antara dua panggilan konkuren).
-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX idx_shifts_one_open ON shifts (site_id) WHERE status = 'OPEN';
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS idx_shifts_one_open;
