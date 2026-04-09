.PHONY: build build-api-bin build-connector-bin build-connector-release build-deploy-bundle dev-api dev-ui dev-connector dev-up dev-seed test-matrix check-env test lint migrate clean smoke-api smoke-api-extended validate-contract

# --- Build ---

build: build-api build-connector build-ui

build-api-bin:
	mkdir -p .gocache bin
	cd apps/api && CGO_ENABLED=0 GOCACHE=$(CURDIR)/.gocache go build -o $(CURDIR)/bin/accessd ./cmd/server

build-connector-bin:
	mkdir -p .gocache bin
	cd apps/connector && CGO_ENABLED=0 GOCACHE=$(CURDIR)/.gocache go build -o $(CURDIR)/bin/accessd-connector ./cmd/connector

build-api:
	cd apps/api && go build -o bin/accessd ./cmd/server

build-connector:
	cd apps/connector && go build -o bin/accessd-connector ./cmd/connector

build-connector-release:
	./scripts/build_connector_release.sh $(VERSION)

build-deploy-bundle:
	./scripts/build_deploy_bundle.sh $(VERSION)

build-ui:
	cd apps/ui && npm run build

# --- Dev ---

dev-api:
	./scripts/dev_api.sh

dev-ui:
	./scripts/dev_ui.sh

dev-connector:
	./scripts/dev_connector.sh

dev-up:
	./scripts/dev_up.sh

dev-up-targets:
	./scripts/dev_up.sh --with-targets

dev-up-targets-mssql:
	./scripts/dev_up.sh --with-targets --with-mssql

dev-seed:
	./scripts/dev_seed.sh

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

smoke-api-extended:
	./scripts/test_api_smoke_extended.sh

test-matrix:
	./scripts/test_matrix.sh

check-env:
	./scripts/check_env.sh

# --- Docker (dev only) ---

up:
	docker-compose up -d

down:
	docker-compose down

# --- Clean ---

clean:
	rm -rf apps/api/bin apps/connector/bin apps/ui/dist deploy/artifacts dist/connector
