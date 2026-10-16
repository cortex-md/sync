.PHONY: build run test test-unit test-e2e test-integration sqlc lint fmt migrate-up migrate-down migrate-create docker-up docker-down clean

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
	go test ./internal/... -race -count=1

test-e2e:
	docker compose --profile replicas up -d --wait postgres s3 mailpit || (docker compose logs s3; exit 1)
	./test/e2e/run.sh

test-integration:
	CORTEX_TEST_DATABASE_URL="$(DATABASE_URL)" go test -tags integration ./internal/platform/postgres/... -count=1

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate

test-coverage:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .
	goimports -w .

migrate-up:
	CORTEX_DATABASE_URL="$(DATABASE_URL)" go run ./cmd/server migrate up

migrate-down:
	CORTEX_DATABASE_URL="$(DATABASE_URL)" go run ./cmd/server migrate down 1

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
