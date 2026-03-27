.PHONY: generate build run-identity run-discovery run-routing run-transfer tidy lint test docker-up docker-down

# Proto code generation (requires buf CLI: https://buf.build/docs/installation)
generate: generate-proto generate-sql

generate-proto:
	buf generate

generate-sql:
	sqlc generate

# Build all services
build:
	go build -o bin/identity ./cmd/identity/...
	go build -o bin/discovery ./cmd/discovery/...
	go build -o bin/routing ./cmd/routing/...
	go build -o bin/transfer ./cmd/transfer/...
	go build -o bin/gateway ./cmd/gateway/...

# Run individual services (requires generated proto files)
run-identity:
	go run ./cmd/identity/...

run-discovery:
	go run ./cmd/discovery/...

run-routing:
	go run ./cmd/routing/...

run-transfer:
	go run ./cmd/transfer/...

# Sync dependencies
tidy:
	go mod tidy

# Tests
test:
	go test ./... -v -race -count=1

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# Lint (requires golangci-lint)
lint:
	golangci-lint run ./...

# Docker
docker-up:
	docker compose -f deployments/docker/docker-compose.yml up --build -d

docker-down:
	docker compose -f deployments/docker/docker-compose.yml down

# Clean build artifacts
clean:
	rm -rf bin/ coverage.out

