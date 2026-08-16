-- Task 5.1 — penomoran tiket atomik per site untuk repository pgx.
--
-- memstore.Next() memakai counter in-process (aman karena satu mutex proses). Repository
-- pgx butuh padanan yang aman terhadap kasir/gerbang konkuren TANPA celah nomor kembar atau
-- kompetisi race pada SELECT-lalu-UPDATE biasa. UPDATE ... SET counter = counter + 1 RETURNING
-- memakai row lock implisit PostgreSQL — aman untuk transaksi konkuren tanpa SELECT FOR UPDATE
-- terpisah.
--
-- Bukan SEQUENCE bawaan PostgreSQL karena penomoran tiket bersifat per-site (site baru = mulai
-- dari 1), sedangkan SEQUENCE global lintas-site akan membocorkan volume antar lahan lewat nomor
-- tiket.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE ticket_counters (
    site_id UUID PRIMARY KEY REFERENCES sites(id),
    counter BIGINT NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS ticket_counters;
