.PHONY: tidy build api obdctl mock-scan migrate enrich-seed enrich-drain enrich-stats test

tidy:
	go mod tidy

build: tidy
	go build -o bin/api ./cmd/api
	go build -o bin/obdctl ./cmd/obdctl
	go build -o bin/enrichctl ./cmd/enrichctl

api: tidy
	go run ./cmd/api

obdctl: tidy
	go run ./cmd/obdctl

mock-scan: tidy
	go run ./cmd/obdctl --mock --no-upload

migrate:
	PGPASSWORD=autoservice psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/001_init.sql
	PGPASSWORD=autoservice psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/002_nigeria_enrichment.sql
	PGPASSWORD=autoservice psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/003_avensis_primary.sql
	PGPASSWORD=autoservice psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/004_remove_vin_overrides.sql
	PGPASSWORD=autoservice psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/005_observations.sql

enrich-stats:
	go run ./cmd/enrichctl --stats

enrich-seed:
	go run ./cmd/enrichctl --seed

enrich-drain:
	go run ./cmd/enrichctl --seed --drain --timeout 25m

test: tidy
	go test ./...