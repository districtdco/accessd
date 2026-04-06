.PHONY: build dev-api dev-ui test lint migrate clean smoke-api validate-contract

# --- Build ---

build: build-api build-connector build-ui

build-api:
	cd apps/api && go build -o bin/pam-server ./cmd/server

build-connector:
	cd apps/connector && go build -o bin/pam-connector ./cmd/connector

build-ui:
	cd apps/ui && npm run build

# --- Dev ---

dev-api:
	cd apps/api && go run ./cmd/server

dev-ui:
	cd apps/ui && npm run dev

# --- Test ---

test: test-api test-ui

test-api:
	cd apps/api && go test ./...

test-connector:
	cd apps/connector && go test ./...

test-ui:
	cd apps/ui && npm test

# --- Contracts ---

validate-contract:
	npx --yes @apidevtools/swagger-cli validate packages/contracts/api.yaml

# --- Lint ---

lint: lint-api lint-ui

lint-api:
	cd apps/api && go vet ./...

lint-ui:
	cd apps/ui && npm run lint

# --- Database ---

migrate:
	cd apps/api && go run ./cmd/server migrate

migrate-down:
	cd apps/api && go run ./cmd/server migrate-down

# --- Smoke ---

smoke-api:
	./scripts/smoke_api.sh

# --- Docker (dev only) ---

up:
	docker-compose up -d

down:
	docker-compose down

# --- Clean ---

clean:
	rm -rf apps/api/bin apps/connector/bin apps/ui/dist
