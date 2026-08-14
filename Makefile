.PHONY: tidy build api obdctl bayui bayui-windows mock-scan migrate enrich-seed enrich-drain enrich-stats test install installer deb

tidy:
	go mod tidy

build: tidy
	go build -o bin/api ./cmd/api
	go build -o bin/obdctl ./cmd/obdctl
	go build -o bin/enrichctl ./cmd/enrichctl
	go build -o bin/bayui ./cmd/bayui

install: build
	install -d $(DESTDIR)/usr/local/bin
	install -m 755 bin/api $(DESTDIR)/usr/local/bin/mechmind-api
	install -m 755 bin/obdctl $(DESTDIR)/usr/local/bin/mechmind-obdctl
	install -m 755 bin/enrichctl $(DESTDIR)/usr/local/bin/mechmind-enrichctl
	install -m 755 bin/bayui $(DESTDIR)/usr/local/bin/mechmind-bayui

api: tidy
	go run ./cmd/api

obdctl: tidy
	go run ./cmd/obdctl

bayui: tidy
	go run ./cmd/bayui

# Cross-compile a Windows GUI helper (same local-web architecture).
# USB still needs an ELM327 serial driver on the Windows PC.
bayui-windows: tidy
	GOOS=windows GOARCH=amd64 go build -o bin/mechmind-bayui.exe ./cmd/bayui

# Ubuntu/Debian .deb for technicians (no Go on the target laptop).
deb installer:
	./scripts/package-deb.sh

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
