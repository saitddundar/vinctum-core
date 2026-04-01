# Vinctum Core

Decentralized data courier platform built on Go microservices, gRPC, and libp2p. Data moves node-to-node over encrypted channels with multi-hop relay and automatic rerouting -- no central cloud dependency.

## Architecture

```
                        ┌─────────────┐
                        │ Browser/CLI │
                        └──────┬──────┘
                               │ HTTP REST
                        ┌──────▼──────┐
                        │   Gateway   │ :8080
                        └──┬───┬───┬──┘
                   gRPC    │   │   │   gRPC
           ┌───────────────┘   │   └───────────────┐
           ▼                   ▼                   ▼
    ┌──────────┐       ┌──────────┐        ┌──────────┐
    │ Identity │:50051 │ Routing  │:50053  │ Transfer │:50054
    └──────────┘       └────┬─────┘        └────┬─────┘
                            │                   │
                       ┌────▼─────┐        ┌────▼─────┐
                       │Discovery │:50052  │  Relay   │:50055
                       └────┬─────┘        └────┬─────┘
                            │                   │
                     ┌──────▼───────────────────▼──────┐
                     │    libp2p Overlay (Kademlia)    │
                     └─────────────────────────────────┘
```

### Services

| Service       | Port  | Transport | Description                                              |
|---------------|-------|-----------|----------------------------------------------------------|
| **Identity**  | 50051 | gRPC      | User registration, JWT auth (access + refresh), blacklist |
| **Discovery** | 50052 | gRPC      | Peer registry, Kademlia DHT bootstrap, peer streaming     |
| **Routing**   | 50053 | gRPC      | Route computation, relay pool management, route tables     |
| **Transfer**  | 50054 | gRPC      | Chunked file transfer with E2E encryption (AES-256-GCM)   |
| **Relay**     | 50055 | gRPC      | Multi-hop chunk forwarding, TTL, rerouting, replication    |
| **Gateway**   | 8080  | HTTP      | REST-to-gRPC proxy, CORS, health checks                   |

### Backing Stores

- **PostgreSQL 16** -- persistent storage for identity, discovery, routing, transfer
- **Redis 7** -- token blacklist and cache (identity service)
- **Filesystem** -- chunk storage for transfer and relay services

## Tech Stack

| Layer      | Technology                           |
|------------|--------------------------------------|
| Language   | Go 1.25                              |
| RPC        | gRPC + Protobuf (buf.build toolchain)|
| P2P        | go-libp2p (Kademlia DHT, mDNS)      |
| Auth       | JWT (HMAC-SHA256) + bcrypt           |
| Database   | PostgreSQL 16 via pgx/v5             |
| Query Gen  | sqlc (type-safe, no ORM)             |
| Cache      | Redis 7 (go-redis)                   |
| Encryption | AES-256-GCM (E2E chunk encryption)   |
| Config     | Viper (YAML + env overlay)           |
| Logging    | Zerolog (structured JSON)            |
| CI/CD      | GitHub Actions                       |
| Container  | Docker + GHCR                        |

## Project Structure

```
vinctum-core/
├── cmd/                        # Service entry points
│   ├── identity/
│   ├── discovery/
│   ├── routing/
│   ├── transfer/
│   └── gateway/
├── services/                   # Business logic per service
│   ├── identity/               #   handler/ + repository/ (sqlc)
│   ├── discovery/              #   handler/ + repository/ (sqlc)
│   ├── routing/                #   handler/ + repository/ (sqlc)
│   ├── transfer/               #   handler/ + repository/ (sqlc) + storage/
│   ├── relay/                  #   handler/ (chunk relay, no DB)
│   └── gateway/                #   handler/ (HTTP routes)
├── proto/                      # Protobuf definitions + generated Go stubs
│   ├── identity/v1/
│   ├── discovery/v1/
│   ├── routing/v1/
│   ├── transfer/v1/
│   ├── relay/v1/
│   └── gateway/v1/
├── internal/                   # Private packages
│   ├── auth/                   #   JWT issuer/validator
│   ├── encryption/             #   AES-256-GCM encrypt/decrypt
│   ├── migrator/               #   Embedded SQL migration runner
│   ├── p2p/                    #   libp2p node (DHT, mDNS)
│   └── relay/                  #   Relay client, rerouter, replicator
├── pkg/                        # Shared public packages
│   ├── config/                 #   Viper config loader
│   ├── crypto/                 #   Hashing utilities
│   ├── logger/                 #   Zerolog setup
│   └── middleware/             #   gRPC auth interceptors
├── scripts/migrations/         # SQL schema files (embedded, auto-applied)
├── config/                     # YAML configs (config.dev.yaml)
├── deployments/docker/         # Dockerfiles + docker-compose.yml
├── docs/                       # Architecture doc, threat model, ADRs, project plan
│   └── adr/                    #   Architecture Decision Records (7 ADRs)
└── .github/workflows/          # CI pipeline
```

## Getting Started

### Prerequisites

- Go 1.25+
- Docker & Docker Compose
- [buf](https://buf.build/docs/installation) (proto generation)
- [sqlc](https://sqlc.dev/) (SQL code generation)

### Quick Start

```bash
# Clone
git clone https://github.com/saitddundar/vinctum-core.git
cd vinctum-core

# Generate code from proto and SQL definitions
make generate

# Start full stack (Postgres + Redis + all services)
make docker-up

# Or run services individually (requires Postgres + Redis running)
docker compose -f deployments/docker/docker-compose.yml up postgres redis -d
make run-identity
make run-gateway
```

### Make Targets

| Target               | Description                          |
|----------------------|--------------------------------------|
| `make generate`      | Run both proto and sqlc generation   |
| `make generate-proto`| Generate Go stubs from .proto files  |
| `make generate-sql`  | Generate Go code from SQL queries    |
| `make build`         | Build all service binaries to `bin/` |
| `make test`          | Run all tests with race detector     |
| `make test-cover`    | Tests + HTML coverage report         |
| `make lint`          | Run golangci-lint                    |
| `make docker-up`     | Build & start all containers         |
| `make docker-down`   | Stop all containers                  |
| `make tidy`          | Sync Go module dependencies          |
| `make clean`         | Remove build artifacts               |

## Testing

```bash
make test          # go test ./... -v -race -count=1
make test-cover    # generates coverage.out + HTML report
```

Handler tests use **fake implementations** of the `Querier` interface (generated by sqlc), so they run without a database. The CI pipeline spins up Postgres + Redis for integration-level coverage.

## CI/CD

GitHub Actions pipeline (`.github/workflows/ci.yml`):

1. **Lint** -- golangci-lint with 5m timeout
2. **Test** -- with Postgres 16 + Redis 7 service containers, race detector, coverage upload
3. **Build** -- matrix build across all 5 services
4. **Docker** -- multi-stage build & push to GHCR on `main` branch

## Data Flow: Chunk Transfer

```
Sender                Transfer              Relay (hop 1)          Relay (hop N)          Receiver
  │                      │                      │                      │                      │
  │── InitiateTransfer ─▶│                      │                      │                      │
  │◀── transfer_id ──────│                      │                      │                      │
  │                      │                      │                      │                      │
  │── SendChunk ────────▶│                      │                      │                      │
  │   (encrypted data)   │── RelayChunk ───────▶│                      │                      │
  │                      │   (TTL, hops[])      │── RelayChunk ───────▶│         ...          │
  │                      │                      │   (TTL-1, hops[1:])  │── store at dest ────▶│
  │                      │                      │                      │                      │
  │                      │                      │   ┌── on failure ──┐ │                      │
  │                      │                      │   │ reroute around │ │                      │
  │                      │                      │   │ failed node    │ │                      │
  │                      │                      │   └────────────────┘ │                      │
```

- Chunks are encrypted with **AES-256-GCM** before transfer
- Each hop verifies **SHA-256** integrity hash
- **TTL** prevents infinite relay loops
- Failed hops trigger **automatic rerouting** via the routing service
- **Replication factor** > 1 stores chunks on multiple nodes

## Security

Key controls:

- JWT (HMAC-SHA256) with Redis-backed token blacklist
- bcrypt password hashing (cost 12)
- Parameterized SQL via sqlc (no injection surface)
- gRPC auth interceptors with method-level bypass for public endpoints
- AES-256-GCM end-to-end chunk encryption
- SHA-256 chunk integrity verification at every hop
- TTL-based relay loop prevention

See [docs/threat_model.md](docs/threat_model.md) for the full STRIDE analysis.

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | C4 diagrams, service responsibilities, deployment topology |
| [Threat Model](docs/threat_model.md) | STRIDE-based security analysis |
| [ADRs](docs/adr/) | 7 Architecture Decision Records |
| [Project Plan](docs/Vinctum_14_Haftalik_Plan.md) | 14-week development roadmap |

## License

MIT
