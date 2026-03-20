# Vinctum Core

Decentralized data courier platform built on microservices and P2P networking.

## Overview

Vinctum eliminates the single point of failure inherent in centralized cloud architectures. Data moves node-to-node over encrypted channels, with relay fallback for NAT traversal scenarios.

## Architecture

```
Client / Mobile App
        |
        v
  [Gateway Service]  --gRPC-->  [Identity Service]
        |
   -----|-------------------------------
   |         |           |            |
   v         v           v            v
[Identity] [Discovery] [Routing] [Transfer]
                           |
                    libp2p / WireGuard
                           |
                    [Remote Node]
```

## Tech Stack

| Layer      | Technology              |
|------------|-------------------------|
| Language   | Go 1.22+                |
| RPC        | gRPC + Protobuf         |
| P2P        | go-libp2p (DHT)         |
| Auth       | JWT + mTLS              |
| VPN        | WireGuard (opt.)        |
| Database   | PostgreSQL via pgx/v5   |
| Query gen  | sqlc                    |
| Cache      | Redis                   |
| Config     | Viper                   |
| Logging    | Zerolog                 |

### What is sqlc?

sqlc is **not an ORM**. You write raw SQL, and sqlc generates type-safe Go code from it — think of it as protobuf for SQL queries. No reflection, no magic, no hidden N+1 queries. The generated code uses pgx directly, so performance is identical to hand-written queries.

## Project Structure

```
vinctum-core/
├── cmd/              # Service entry points
├── services/         # Microservice implementations
│   ├── identity/
│   ├── discovery/
│   ├── routing/
│   ├── transfer/
│   └── gateway/
├── proto/            # Protobuf schemas (.proto + generated .pb.go)
├── pkg/              # Shared packages (config, logger, crypto, middleware)
├── internal/         # Internal packages (auth JWT, token blacklist)
├── deployments/      # Docker Compose & Kubernetes manifests
├── scripts/
│   └── migrations/   # SQL migration files
└── docs/adr/         # Architecture Decision Records
```

## Getting Started

```bash
# Install tools
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# Generate code from proto and SQL
buf generate
sqlc generate

# Start infrastructure
docker compose -f deployments/docker/docker-compose.yml up -d

# Run a service
go run ./cmd/identity/...
```

## Testing

```bash
go test ./...
```

## License

MIT
