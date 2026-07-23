# Perintah umum. Butuh: docker compose, goose, go, python, node.
.PHONY: help db-up db-down migrate migrate-down test test-go test-py grants

help:
	@echo "db-up      - jalankan edge-db & cloud-db (docker compose)"
	@echo "db-down    - hentikan database"
	@echo "migrate    - terapkan migrasi ke edge-db (butuh EDGE_DATABASE_URL & goose)"
	@echo "grants     - terapkan db/grants.sql (append-only privilege audit_logs)"
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

test: test-go test-py

test-go:
	cd services/edge-api && go test ./...
	cd services/cloud-api && go test ./...

test-py:
	cd services/lpr-svc && python -m pytest -q
