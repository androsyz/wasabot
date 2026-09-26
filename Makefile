VERSION ?= dev
MIGRATIONS_DIR := internal/db/migrations

-include .env
export

DB_PATH := $(or $(WASABOT_DB_PATH),wasabot.db)
GOOSE := go tool goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH)

.PHONY: run build test vet check migrate-create migrate-status migrate-up migrate-down migrate-reset

run:
	go run ./cmd/wasabot

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/wasabot ./cmd/wasabot

test:
	go test ./...

vet:
	go vet ./...

check: vet test

migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=add_messages" && exit 1)
	go tool goose -dir $(MIGRATIONS_DIR) -s create $(name) sql

migrate-status:
	$(GOOSE) status

migrate-up:
	$(GOOSE) up

migrate-down:
	$(GOOSE) down

migrate-reset:
	$(GOOSE) reset
