.PHONY: test test-race vet build run db-up db-down web-install web-test web-build

test:
	GOTOOLCHAIN=local go test ./... -count=1

test-race:
	GOTOOLCHAIN=local go test -race ./... -count=1

vet:
	GOTOOLCHAIN=local go vet ./...

build:
	GOTOOLCHAIN=local go build ./...

run:
	GOTOOLCHAIN=local go run ./cmd/api

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

web-install:
	npm --prefix web ci

web-test:
	npm --prefix web test -- --run

web-build:
	npm --prefix web run typecheck
	npm --prefix web run build
