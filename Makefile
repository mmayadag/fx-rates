GOCACHE_DIR := $(CURDIR)/.gocache
GO := env GOTOOLCHAIN=go1.26.2 GOCACHE='$(GOCACHE_DIR)' go
LOCAL_TEST_COMPOSE := docker compose -f local_test/docker-compose.yml
ENV_FILE ?= .env
BACKFILL_CONCURRENCY ?= 10
ENV_LOADER = set -a; if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; set +a;

.PHONY: help build test coverage test-integration run run-daily validate-fx daily-sync-example local-run local-run-daily local-db-up local-db-down local-db-logs local-db-ps local-smoke local-smoke-daily

help:
	@printf '%s\n' \
		'make build             - Build all Go packages' \
		'make test              - Run unit tests' \
		'make coverage          - Show test coverage per package' \
		'make test-integration  - Start local Postgres and run all tests (including DB integration tests)' \
		'make run               - Load .env and run the ECB sync job' \
		'make run-daily         - Load .env and force daily_sync mode' \
		'make validate-fx       - Load .env and validate ECB DB rates against ECB reference data' \
		'make daily-sync-example - Show a daily_sync example with timeout and heartbeat' \
		'make local-run         - Load .env and run against local_test Postgres' \
		'make local-run-daily   - Load .env and run daily_sync against local_test Postgres' \
		'make local-db-up   - Start local Postgres for smoke tests' \
		'make local-db-down - Stop local Postgres' \
		'make local-db-logs - Tail local Postgres logs' \
		'make local-db-ps   - Show local Postgres status' \
		'make local-smoke   - Start local Postgres and run the sync job against it' \
		'make local-smoke-daily - Start local Postgres and run daily_sync against it'

build:
	$(GO) build ./...

test:
	$(GO) test ./...

coverage:
	@$(GO) test ./... -cover -count=1 2>&1 | grep -E "^ok|^FAIL|coverage:"

test-integration: local-db-up
	@echo "Waiting for Postgres to be ready..."
	@until docker exec $$($(LOCAL_TEST_COMPOSE) ps -q postgres) pg_isready -q; do sleep 1; done
	@$(ENV_LOADER) \
	TEST_DATABASE_URL="postgres://$${DB_USER:?DB_USER is required}:$${DB_PASSWORD:?DB_PASSWORD is required}@$${DB_HOST:?DB_HOST is required}:$${DB_PORT:?DB_PORT is required}/$${DB_NAME:?DB_NAME is required}?sslmode=$${DB_SSLMODE:?DB_SSLMODE is required}" \
	GOTOOLCHAIN=go1.26.2 \
	GOCACHE='$(GOCACHE_DIR)' \
	go test ./... -count=1 -timeout 120s

run:
	@$(ENV_LOADER) \
	BACKFILL_CONCURRENCY="$${BACKFILL_CONCURRENCY:-$(BACKFILL_CONCURRENCY)}" \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run .

run-daily:
	@$(ENV_LOADER) \
	SYNC_MODE='daily_sync' \
	BACKFILL_CONCURRENCY="$${BACKFILL_CONCURRENCY:-$(BACKFILL_CONCURRENCY)}" \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run .

validate-fx:
	@$(ENV_LOADER) \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run ./cmd/fx-validate $(ARGS)

daily-sync-example:
	@printf '%s\n' \
		'cp .env.daily_sync.template .env' \
		'make local-run' \
		'make run'

local-run:
	@$(ENV_LOADER) \
	DB_USER="$${DB_USER:?DB_USER is required}" \
	DB_PASSWORD="$${DB_PASSWORD:?DB_PASSWORD is required}" \
	DB_NAME="$${DB_NAME:?DB_NAME is required}" \
	DB_HOST="$${DB_HOST:?DB_HOST is required}" \
	DB_PORT="$${DB_PORT:?DB_PORT is required}" \
	DB_SSLMODE="$${DB_SSLMODE:?DB_SSLMODE is required}" \
	BACKFILL_CONCURRENCY="$${BACKFILL_CONCURRENCY:-$(BACKFILL_CONCURRENCY)}" \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run .

local-run-daily:
	@$(ENV_LOADER) \
	DB_USER="$${DB_USER:?DB_USER is required}" \
	DB_PASSWORD="$${DB_PASSWORD:?DB_PASSWORD is required}" \
	DB_NAME="$${DB_NAME:?DB_NAME is required}" \
	DB_HOST="$${DB_HOST:?DB_HOST is required}" \
	DB_PORT="$${DB_PORT:?DB_PORT is required}" \
	DB_SSLMODE="$${DB_SSLMODE:?DB_SSLMODE is required}" \
	SYNC_MODE='daily_sync' \
	BACKFILL_CONCURRENCY="$${BACKFILL_CONCURRENCY:-$(BACKFILL_CONCURRENCY)}" \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run .

local-db-up:
	$(LOCAL_TEST_COMPOSE) up -d

local-db-down:
	$(LOCAL_TEST_COMPOSE) down

local-db-logs:
	$(LOCAL_TEST_COMPOSE) logs -f postgres

local-db-ps:
	$(LOCAL_TEST_COMPOSE) ps

local-smoke: local-db-up
	@$(ENV_LOADER) \
	DB_USER="$${DB_USER:?DB_USER is required}" \
	DB_PASSWORD="$${DB_PASSWORD:?DB_PASSWORD is required}" \
	DB_NAME="$${DB_NAME:?DB_NAME is required}" \
	DB_HOST="$${DB_HOST:?DB_HOST is required}" \
	DB_PORT="$${DB_PORT:?DB_PORT is required}" \
	DB_SSLMODE="$${DB_SSLMODE:?DB_SSLMODE is required}" \
	BACKFILL_CONCURRENCY="$${BACKFILL_CONCURRENCY:-$(BACKFILL_CONCURRENCY)}" \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run .

local-smoke-daily: local-db-up
	@$(ENV_LOADER) \
	DB_USER="$${DB_USER:?DB_USER is required}" \
	DB_PASSWORD="$${DB_PASSWORD:?DB_PASSWORD is required}" \
	DB_NAME="$${DB_NAME:?DB_NAME is required}" \
	DB_HOST="$${DB_HOST:?DB_HOST is required}" \
	DB_PORT="$${DB_PORT:?DB_PORT is required}" \
	DB_SSLMODE="$${DB_SSLMODE:?DB_SSLMODE is required}" \
	SYNC_MODE='daily_sync' \
	BACKFILL_CONCURRENCY="$${BACKFILL_CONCURRENCY:-$(BACKFILL_CONCURRENCY)}" \
	GOTOOLCHAIN='go1.26.2' \
	GOCACHE='$(GOCACHE_DIR)' \
	go run .
