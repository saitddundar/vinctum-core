# Vinctum Core

Decentralized data courier platform built on Go microservices, gRPC, and libp2p. Data moves node-to-node over encrypted channels with multi-hop relay and automatic rerouting -- no central cloud dependency.

## Architecture

```
                        ┌─────────────┐
                        │ Browser/CLI │
                        └──────┬──────┘
                               │ HTTP REST + NDJSON streams
                        ┌──────▼──────┐
                        │   Gateway   │ :8080
                        └──┬───┬───┬──┘
                   gRPC    │   │   │   gRPC
           ┌───────────────┘   │   └────────────────────────┐
           ▼                   ▼                            ▼
    ┌──────────┐         ┌──────────┐            ┌────────────────────┐
    │ Identity │ :50051  │ Routing  │ :50053     │ Transfer + Relay   │ :50054
    └──────────┘         └────┬─────┘            │ (co-hosted process)│
                              │                  └─────────┬──────────┘
                              │                            │
                         ┌────▼─────┐                      │
                         │Discovery │ :50052               │
                         └────┬─────┘                      │
                              │                            │
                       ┌──────▼────────────────────────────▼──────┐
                       │        libp2p Overlay (Kademlia)         │
                       └──────────────────────────────────────────┘
```

Each gRPC service also exposes a Prometheus `/metrics` endpoint on `grpcPort + 1000` (e.g. identity at `:51051/metrics`); the gateway exposes `/metrics` on `:8080`.

### Services

| Service       | Port  | Transport | Description                                                                                |
|---------------|-------|-----------|--------------------------------------------------------------------------------------------|
| **Identity**  | 50051 | gRPC      | Users, JWT auth, email verification, devices, pairing, peer sessions, X25519 device keys   |
| **Discovery** | 50052 | gRPC      | Peer registry, Kademlia DHT bootstrap, peer streaming                                      |
| **Routing**   | 50053 | gRPC      | Route computation, relay pool management, intelligence-aware path scoring                  |
| **Transfer**  | 50054 | gRPC      | Chunk-based file transfer (server holds only ciphertext), watch streams, in-process Relay  |
| **Gateway**   | 8080  | HTTP      | REST-to-gRPC proxy, NDJSON event streams, CORS, security headers, health checks            |

> The Relay service (`services/relay/handler`) runs **inside** the transfer process — it shares the same gRPC server on `:50054` rather than binding its own port. Multi-hop forwarding, TTL handling, rerouting, and replication all live there.

### Backing Stores

- **PostgreSQL 16** -- persistent storage for identity, discovery, routing, transfer (one logical DB per service, schemas auto-applied via embedded migrations in `scripts/migrations/`)
- **Redis 7** -- JWT blacklist + short-lived pairing codes (identity service)
- **Filesystem** -- encrypted chunk store for transfer/relay (`VINCTUM_CHUNK_DIR`, default `./data/chunks`)
- **SMTP (optional)** -- email verification delivery via `pkg/mailer`

## Tech Stack

| Layer        | Technology                                              |
|--------------|---------------------------------------------------------|
| Language     | Go 1.25                                                 |
| RPC          | gRPC + Protobuf (buf.build toolchain)                   |
| P2P          | go-libp2p (Kademlia DHT, mDNS)                          |
| Auth         | JWT (HMAC-SHA256) + bcrypt + Redis-backed blacklist     |
| Database     | PostgreSQL 16 via pgx/v5                                |
| Query Gen    | sqlc (type-safe, no ORM, hand-written queries)          |
| Cache        | Redis 7 (go-redis) — JWT blacklist + pairing codes      |
| Encryption   | AES-256-GCM (E2E chunk encryption, client-side enforced)|
| Key Exchange | X25519 (per-device static keys, registered with Identity)|
| TLS          | Optional mTLS between gRPC services via `pkg/grpcutil`  |
| Metrics      | Prometheus (per-service `/metrics` on `grpcPort+1000`)  |
| Config       | Viper (YAML + `VINCTUM_*` env overlay)                  |
| Logging      | Zerolog (structured JSON)                               |
| Mail         | SMTP via `pkg/mailer` (verification emails)             |
| CI/CD        | GitHub Actions                                          |
| Container    | Docker + GHCR multi-stage builds                        |

## Project Structure

```
vinctum-core/
├── cmd/                        # Service entry points (5 binaries)
│   ├── identity/               #   relay handler is co-hosted in transfer
│   ├── discovery/
│   ├── routing/
│   ├── transfer/               #   also serves relay.v1.RelayService
│   └── gateway/
├── services/                   # Business logic per service
│   ├── identity/               #   handler/ + repository/ (sqlc)
│   ├── discovery/              #   handler/ + repository/ (sqlc)
│   ├── routing/                #   handler/ + repository/ (sqlc)
│   ├── transfer/               #   handler/ + repository/ (sqlc) + storage/ (chunk store)
│   ├── relay/                  #   handler/ (chunk relay, no DB, hosted by transfer)
│   └── gateway/                #   handler/ (HTTP routes + NDJSON streams)
├── proto/                      # Protobuf definitions + generated Go stubs
│   ├── identity/v1/
│   ├── discovery/v1/
│   ├── routing/v1/
│   ├── transfer/v1/
│   ├── relay/v1/
│   └── gateway/v1/
├── internal/                   # Private packages
│   ├── auth/                   #   JWT issuer/validator + Redis blacklist + pairing codes
│   ├── encryption/             #   AES-256-GCM helpers
│   ├── intelligence/           #   Node scoring, anomaly detection, ML adapter
│   ├── migrator/               #   Embedded SQL migration runner
│   ├── p2p/                    #   libp2p node (DHT, mDNS)
│   └── relay/                  #   Relay client, rerouter, replicator
├── pkg/                        # Shared public packages
│   ├── config/                 #   Viper config loader
│   ├── crypto/                 #   Hashing + token utilities
│   ├── grpcutil/               #   gRPC TLS/mTLS credential loading
│   ├── logger/                 #   Zerolog setup
│   ├── mailer/                 #   SMTP verification mailer
│   └── middleware/             #   gRPC auth + Prometheus metrics interceptors
├── scripts/migrations/         # SQL schema files (010 migrations, embedded, auto-applied)
├── config/                     # YAML configs (config.dev.yaml)
├── deployments/docker/         # Dockerfiles (5 services) + docker-compose.yml
├── docs/                       # Architecture doc, threat model, ADRs, project plan
│   └── adr/                    #   Architecture Decision Records (7 ADRs)
└── .github/workflows/          # CI pipeline (lint → test → build → docker push)
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

## HTTP API (Gateway)

All routes are exposed by the gateway on `:8080`. JWT bearer tokens are required except where noted.

| Group         | Method & Path                                   | Notes                                  |
|---------------|-------------------------------------------------|----------------------------------------|
| Health        | `GET  /health`, `GET  /services`                | Public                                 |
| Auth          | `POST /api/v1/auth/register`                    | Public                                 |
|               | `POST /api/v1/auth/login`                       | Public, requires verified email        |
|               | `POST /api/v1/auth/refresh`                     | Public                                 |
|               | `POST /api/v1/auth/validate`                    | Public                                 |
|               | `POST /api/v1/auth/verify`                      | Public, email verification token       |
|               | `POST /api/v1/auth/resend-verification`         | Public                                 |
| Devices       | `POST /api/v1/devices`                          | Self-register current device           |
|               | `GET  /api/v1/devices`                          | List own devices                       |
|               | `GET  /api/v1/devices/{deviceId}`               |                                        |
|               | `DELETE /api/v1/devices/{deviceId}`             | Revoke                                 |
|               | `PUT  /api/v1/devices/{deviceId}/activity`      | Heartbeat                              |
| Pairing       | `POST /api/v1/devices/pairing/generate`         | Issue 6-char code (5 min TTL)          |
|               | `POST /api/v1/devices/pairing/redeem`           | Redeem code, create pending device     |
|               | `POST /api/v1/devices/pairing/approve`          | Approver-side accept/reject            |
| Sessions      | `POST /api/v1/sessions`                         | Create peer session                    |
|               | `GET  /api/v1/sessions`                         |                                        |
|               | `POST /api/v1/sessions/{sessionId}/close`       |                                        |
|               | `POST /api/v1/sessions/{sessionId}/join`        |                                        |
|               | `POST /api/v1/sessions/{sessionId}/leave`       |                                        |
|               | `GET  /api/v1/sessions/{sessionId}/devices`     |                                        |
| Device Keys   | `POST /api/v1/devices/{deviceId}/key`           | Upload X25519 public key (32B)         |
|               | `GET  /api/v1/devices/{deviceId}/key`           |                                        |
|               | `GET  /api/v1/sessions/{sessionId}/keys`        | All device keys in a session           |
| Routing       | `POST /api/v1/routes/find`                      |                                        |
|               | `GET  /api/v1/routes/table/{nodeId}`            |                                        |
|               | `GET  /api/v1/relays`                           |                                        |
| Intelligence  | `GET  /api/v1/ml/health`                        | Forwards to ML service                 |
|               | `POST /api/v1/ml/score`, `/anomaly`, `/route`   |                                        |
| Transfers     | `POST /api/v1/transfers`                        | Initiate (ciphertext only, client E2E) |
|               | `GET  /api/v1/transfers/{transferId}`           |                                        |
|               | `GET  /api/v1/transfers/node/{nodeId}`          |                                        |
|               | `POST /api/v1/transfers/{transferId}/cancel`    |                                        |
|               | `POST /api/v1/transfers/{transferId}/chunks`    | Upload encrypted chunk                 |
|               | `GET  /api/v1/transfers/{transferId}/chunks`    | Download encrypted chunks              |
|               | `GET  /api/v1/transfers/watch`                  | Long-lived NDJSON event stream         |
| Metrics       | `GET  /metrics`                                 | Prometheus, gateway-level              |

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
  │── UploadChunk ──────▶│                      │                      │                      │
  │   (ciphertext +      │── RelayChunk ───────▶│                      │                      │
  │   sha256 hash)       │   (TTL, hops[])      │── RelayChunk ───────▶│         ...          │
  │                      │                      │   (TTL-1, hops[1:])  │── store at dest ────▶│
  │                      │                      │                      │                      │
  │                      │                      │   ┌── on failure ──┐ │                      │
  │                      │                      │   │ reroute around │ │                      │
  │                      │                      │   │ failed node    │ │                      │
  │                      │                      │   └────────────────┘ │                      │
  │                                                                                            │
  │── (optional) GET /api/v1/transfers/watch  ──────────────  NDJSON event stream  ──────────▶ │
```

- **Encryption is client-side and end-to-end** — chunks reach the transfer service already wrapped in AES-256-GCM. The server stores only ciphertext and never sees plaintext or symmetric keys (enforced since commit a9a5771).
- **Per-device X25519 public keys** are registered with Identity (`device_keys`); the sender derives a per-transfer symmetric key by combining its ephemeral X25519 secret with the receiver's static public key (transfer-side wiring lands in the next phase).
- **SHA-256** integrity hash is mandatory on every chunk and verified at every hop.
- **TTL** prevents infinite relay loops; failed hops trigger **automatic rerouting** via the routing service.
- **Replication factor** > 1 stores chunks on multiple nodes.
- Clients can subscribe to long-lived **NDJSON streams** (`/api/v1/transfers/watch`) to react to transfer state changes in near-real-time.

## Security

Key controls:

- **JWT (HMAC-SHA256)** access + refresh tokens, Redis-backed blacklist for revocation
- **bcrypt** password hashing (cost 12, configurable)
- **Mandatory email verification** before login (SMTP via `pkg/mailer`)
- **Multi-device pairing** with short-lived 6-character codes (Redis, 5 min TTL) and approver-side accept/reject
- **Per-device X25519 static keys** registered with Identity (`device_keys` table, migration 010); the server only stores ciphertext for transfers and never holds plaintext or symmetric keys
- **AES-256-GCM** end-to-end chunk encryption performed entirely on the client
- **SHA-256** chunk integrity verification at every hop (mandatory, not optional)
- **TTL-based** relay loop prevention + transfer ownership checks on download
- **Parameterized SQL** via sqlc (no injection surface, no hand-rolled string concatenation)
- **gRPC auth interceptors** on every service with method-level bypass only for public endpoints (Register/Login/RefreshToken/VerifyEmail/ResendVerification + Discovery FindPeers/GetNodeInfo)
- **Optional mTLS** between gRPC services via `pkg/grpcutil` (`grpc.tls_enabled: true` + cert/key/CA)
- **Security headers + CORS whitelist + body size limits** on the gateway
- **Prometheus metrics** on every service for auditing latency / error rates

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
