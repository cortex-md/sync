.PHONY: build run test test-unit test-integration test-e2e lint fmt migrate-up migrate-down migrate-create docker-up docker-down clean

BINARY_NAME=cortex-sync
BUILD_DIR=./bin
MIGRATIONS_DIR=./migrations
DATABASE_URL?=postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable

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
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

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
