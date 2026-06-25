.PHONY: test test-int db-up db-down db-logs

# Default test run: unit + sqlite, no external databases, fast.
test:
	go test ./...

# Integration run: requires the live databases (make db-up first). Tests skip a
# driver whose DSN is unreachable. Override DSNs via the PLAYSQL_*_DSN env vars.
test-int:
	go test -tags integration ./...

# Bring the databases up and wait for health.
db-up:
	docker compose up -d --wait

db-down:
	docker compose down -v

db-logs:
	docker compose logs -f
