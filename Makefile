.PHONY: build run test test-unit test-integration test-e2e lint fmt migrate-up migrate-down migrate-create docker-up docker-down clean

BINARY_NAME=cortex-sync
BUILD_DIR=./bin
MIGRATIONS_DIR=./migrations
DATABASE_URL?=postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable
ENV_FILE?=.env.local

-include $(ENV_FILE)
export

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

test:
	go test ./... -v -race -count=1

test-unit:
	go test ./internal/domain/... ./internal/usecase/... -v -race -count=1

test-integration:
	go test ./test/integration/... -v -race -count=1 -tags=integration

test-e2e:
	go test ./test/e2e/... -v -race -count=1 -tags=e2e

test-coverage:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .
	goimports -w .

migrate-up:
	CORTEX_DATABASE_URL="$(DATABASE_URL)" CORTEX_DATABASE_MIGRATIONS_PATH=file://$(MIGRATIONS_DIR) go run ./cmd/server migrate up

migrate-down:
	CORTEX_DATABASE_URL="$(DATABASE_URL)" CORTEX_DATABASE_MIGRATIONS_PATH=file://$(MIGRATIONS_DIR) go run ./cmd/server migrate down 1

migrate-create:
	@read -p "Migration name: " name; \
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $$name

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-reset:
	docker compose down -v
	docker compose up -d

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html
