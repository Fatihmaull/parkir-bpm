# Perintah umum. Butuh: goose, psql, go, python, node. db-up/db-down butuh docker compose
# HANYA bila kamu memilih Postgres lokal via Docker — lihat catatan di db-up. Alternatif tanpa
# Docker: Postgres lokal biasa (`apt install postgresql`) atau Postgres cloud dev (mis. Neon);
# migrate/grants/seed sama-sama cukup dengan EDGE_DATABASE_URL, tak peduli dari mana DB-nya.
.PHONY: help db-up db-down migrate migrate-down seed test test-go test-py grants

help:
	@echo "db-up      - jalankan edge-db & cloud-db (docker compose) -- opsional, lihat komentar di atas"
	@echo "db-down    - hentikan database (docker compose)"
	@echo "migrate    - terapkan migrasi ke edge-db (butuh EDGE_DATABASE_URL & goose)"
	@echo "grants     - terapkan db/grants.sql (append-only privilege audit_logs)"
	@echo "seed       - isi data dev multi-lahan/multi-gerbang (task 5.4, butuh EDGE_DATABASE_URL)"
	@echo "test       - jalankan seluruh unit test"

db-up:
	docker compose up -d edge-db cloud-db

db-down:
	docker compose down

migrate:
	goose -dir db/migrations postgres "$(EDGE_DATABASE_URL)" up

migrate-down:
	goose -dir db/migrations postgres "$(EDGE_DATABASE_URL)" down

grants:
	psql "$(EDGE_DATABASE_URL)" -f db/grants.sql

seed:
	psql "$(EDGE_DATABASE_URL)" -f db/seed/dev_seed.sql

test: test-go test-py

test-go:
	cd services/edge-api && go test ./...
	cd services/cloud-api && go test ./...

test-py:
	cd services/lpr-svc && python -m pytest -q
