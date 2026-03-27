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

### Services

| Service | Port | Description |
|---------|------|-------------|
| **Identity** | 50051 | User registration, JWT authentication (access/refresh tokens), token blacklist |
| **Discovery** | 50052 | P2P node discovery, peer registry, Kademlia DHT bootstrap |
| **Routing** | 50053 | Message routing, relay selection, route table management |
| **Transfer** | 50054 | Chunked file/data transfer with E2E encryption support |
| **Gateway** | — | REST-to-gRPC gateway *(planned)* |

## Tech Stack

| Layer      | Technology              |
|------------|-------------------------|
| Language   | Go 1.23+                |
| RPC        | gRPC + Protobuf (buf)   |
| P2P        | go-libp2p (Kademlia DHT, mDNS) |
| Auth       | JWT (HMAC-SHA256) + bcrypt |
| VPN        | WireGuard (opt.)        |
| Database   | PostgreSQL 16 via pgx/v5 |
| Query gen  | sqlc                    |
| Cache      | Redis 7                 |
| Config     | Viper                   |
| Logging    | Zerolog                 |
| CI/CD      | GitHub Actions          |
| Container  | Docker + GHCR           |

### What is sqlc?

sqlc is **not an ORM**. You write raw SQL, and sqlc generates type-safe Go code from it — think of it as protobuf for SQL queries. No reflection, no magic, no hidden N+1 queries. The generated code uses pgx directly, so performance is identical to hand-written queries.

## Project Structure

```
vinctum-core/
├── cmd/                  # Service entry points (one main.go per service)
│   ├── identity/
│   ├── discovery/
│   ├── routing/
│   └── transfer/
├── services/             # Microservice implementations
│   ├── identity/         # handler + repository (sqlc)
│   ├── discovery/
│   ├── routing/
│   ├── transfer/
│   └── gateway/
├── proto/                # Protobuf schemas (.proto + generated .pb.go)
│   ├── identity/v1/
│   ├── discovery/v1/
│   ├── routing/v1/
│   └── transfer/v1/
├── pkg/                  # Shared packages (config, logger, crypto, middleware)
├── internal/             # Internal packages (auth JWT, token blacklist, p2p, migrator)
├── deployments/
│   └── docker/           # Docker Compose + Dockerfiles
├── scripts/
│   └── migrations/       # SQL migration files (auto-applied on startup)
├── doc/                  # Project docs (14-week plan, threat model)
└── .github/workflows/    # CI/CD pipeline (lint → test → build → docker push)
```

## Getting Started

### Prerequisites

- Go 1.23+
- Docker & Docker Compose
- [buf](https://buf.build/docs/installation) (proto generation)
- [sqlc](https://sqlc.dev/) (SQL code generation)

### Quick Start

```bash
# Generate code from proto and SQL
make generate

# Start all infrastructure + services
make docker-up

# Or run a single service locally (requires Postgres + Redis)
docker compose -f deployments/docker/docker-compose.yml up postgres redis -d
make run-identity
```

### Available Make Targets

| Target | Description |
|--------|-------------|
| `make generate` | Generate proto + sqlc code |
| `make build` | Build all service binaries |
| `make run-identity` | Run identity service locally |
| `make run-discovery` | Run discovery service locally |
| `make run-routing` | Run routing service locally |
| `make run-transfer` | Run transfer service locally |
| `make test` | Run all tests with race detector |
| `make test-cover` | Run tests with coverage report |
| `make lint` | Run golangci-lint |
| `make docker-up` | Build & start all containers |
| `make docker-down` | Stop all containers |
| `make tidy` | Sync Go module dependencies |
| `make clean` | Remove build artifacts |

## Testing

```bash
# Run all tests
make test

# Run with coverage
make test-cover
```

## CI/CD

The project uses GitHub Actions with the following pipeline:

1. **Lint** — golangci-lint
2. **Test** — with Postgres + Redis service containers, race detector enabled
3. **Build** — matrix build for all services
4. **Docker** — build & push to GHCR on `main` branch

## Security

See [doc/threat_model.md](doc/threat_model.md) for the full STRIDE-based security threat model.

## License

MIT
