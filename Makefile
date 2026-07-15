SHELL := /bin/bash
# --project-directory . so compose reads the same repo-root .env that the Go
# services read. Without it compose would look for .env next to the compose
# file, and the containers and the app could disagree about a port.
COMPOSE := docker compose --project-directory . -f deploy/docker-compose.yml

.DEFAULT_GOAL := help

## help: list available targets
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed -e 's/## //' | awk -F':' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## up: start the backing services and wait for them to be healthy
# minio-init is deliberately not in the --wait set: it is a one-shot container
# that creates the bucket and exits 0, and `--wait` treats any exited container
# as a failed one. It runs after, as a job.
up:
	$(COMPOSE) up -d --wait postgres clickhouse redis minio
	$(COMPOSE) run --rm minio-init
	@echo "stack is healthy — run 'make migrate' next"

## down: stop the stack, keeping data
down:
	$(COMPOSE) down

## reset: stop the stack and delete every volume. Destroys all local data.
reset:
	$(COMPOSE) down -v

## logs: follow logs from the stack
logs:
	$(COMPOSE) logs -f

## migrate: apply Postgres and ClickHouse schemas
migrate:
	go run ./cmd/migrate

## migrate-status: show applied and pending migrations, changing nothing
migrate-status:
	go run ./cmd/migrate -status

## build: compile every binary into ./bin
build:
	go build -o bin/ ./cmd/...

## test: run the Go test suite
test:
	go test ./... -race -count=1

## lint: vet and check formatting
lint:
	go vet ./...
	@unformatted=$$(gofmt -l . | grep -v '^$$' || true); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

## fmt: format the Go tree
fmt:
	go fmt ./...

## tidy: tidy go.mod
tidy:
	go mod tidy

## dev: bring the stack up and migrate it — one command from clean checkout
dev: up migrate

.PHONY: help up down reset logs migrate migrate-status build test lint fmt tidy dev seed gateway processor api alerter

## seed: create an org, project and ingest key; print the ingest URL
seed:
	go run ./cmd/seed

## gateway: run the ingest gateway
gateway:
	go run ./cmd/gateway

## processor: run the queue consumer
processor:
	go run ./cmd/processor

## api: run the dashboard API
api:
	go run ./cmd/api

## alerter: run the alert evaluation service
alerter:
	go run ./cmd/alerter
