# Vinctum

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-active--development-orange)](#development-status)
[![CI](https://github.com/saitddundar/vinctum-core/actions/workflows/ci.yml/badge.svg)](https://github.com/saitddundar/vinctum-core/actions)
[![Views](https://hits.sh/github.com/saitddundar/vinctum-core.svg?label=Views&color=003580&labelColor=555555)](https://hits.sh/github.com/saitddundar/vinctum-core/)

A decentralized data courier platform built with Go microservices, gRPC, and libp2p. Files move device-to-device over encrypted channels with multi-hop relay and automatic rerouting — no central cloud stores your data.

## Table of Contents

- [Overview](#overview)
- [Key Features](#key-features)
- [Architecture](#architecture)
- [Project Structure](#project-structure)
- [End-to-End Encryption](#end-to-end-encryption)
- [Chunk Transfer Protocol](#chunk-transfer-protocol)
- [Relay & Rerouting](#relay--rerouting)
- [Intelligent Routing](#intelligent-routing)
- [Getting Started](#getting-started)
- [Configuration](#configuration)
- [Security](#security)
- [Observability](#observability)
- [HTTP API Reference](#http-api-reference)
- [Testing](#testing)
- [CI/CD](#cicd)
- [Makefile Commands](#makefile-commands)
- [Architecture Decision Records](#architecture-decision-records)
- [Development Status](#development-status)
- [License](#license)

## Overview

Vinctum is a self-hosted, zero-knowledge file transfer platform. Instead of uploading files to a cloud provider, data travels directly between your devices through an encrypted relay mesh. The server never sees plaintext — it only forwards ciphertext chunks it cannot decrypt.

**The problem:** Cloud file sharing means trusting a third party with your data. Even "encrypted" services often hold the keys.

**The solution:** Vinctum separates the transport layer from the encryption layer. A microservices control plane handles auth, routing, and relay coordination, while encryption keys are derived and used exclusively on the client side. The server is a courier, not a vault.

| Aspect | How Vinctum handles it |
|--------|----------------------|
| **Storage** | No cloud storage — chunks relay through nodes and land on the receiver's device |
| **Encryption** | Client-side AES-256-GCM; server holds only ciphertext (enforced architecturally) |
| **Key Exchange** | Per-device X25519 static keys + sender-ephemeral ECDH + HKDF-SHA256 |
| **Routing** | Multi-hop relay with automatic rerouting around failed nodes |
| **Discovery** | libp2p Kademlia DHT for decentralized peer discovery |
| **Intelligence** | ML-assisted node scoring and anomaly detection for optimal path selection |

## Key Features

- **Zero-knowledge transfer** — server stores and forwards only ciphertext; symmetric keys never leave the client
- **End-to-end encryption** — AES-256-GCM with per-transfer key derivation via X25519 ECDH + HKDF-SHA256
- **Multi-hop relay** — chunks traverse multiple relay nodes with TTL-based loop prevention
- **Automatic rerouting** — failed relay nodes are bypassed via alternative routes from the routing service
- **Circuit breaker** — per-node circuit breakers prevent cascading failures in the relay mesh
- **Chunk replication** — configurable replication factor stores chunks on multiple nodes for redundancy
- **Intelligent routing** — ML-powered node scoring (latency, uptime, throughput, stability) with Z-score anomaly detection
- **Device pairing** — 6-character pairing codes (crypto/rand, Redis, 5-min TTL) with approver-side accept/reject
- **Friend system** — friend requests, friend device discovery (only public + approved + non-revoked devices)
- **Real-time streaming** — NDJSON event streams for transfer state changes and peer updates
- **Audit logging** — every gRPC call recorded (user, method, status, peer, duration) for full observability
- **Email verification** — mandatory email verification before login, with SMTP delivery
- **mTLS support** — optional mutual TLS between all gRPC services

## Architecture

```
                             ┌──────────────────┐
                             │  Browser / CLI   │
                             └────────┬─────────┘
                                      │ HTTP REST + NDJSON streams
                             ┌────────▼─────────┐
                             │     Gateway      │ :8080
                             │  (REST → gRPC)   │
                             └──┬────┬────┬──┬──┘
                        gRPC    │    │    │   │   gRPC
              ┌─────────────────┘    │    │   └──────────────────────┐
              ▼                      ▼    ▼                         ▼
   ┌───────────────────┐   ┌──────────┐  ┌──────────┐   ┌────────────────────┐
   │  Identity Service │   │ Routing  │  │Discovery │   │ Transfer + Relay   │
   │      :50051       │   │  :50053  │  │  :50052  │   │ (co-hosted) :50054 │
   │                   │   └────┬─────┘  └────┬─────┘   └──────────┬─────────┘
   │ • Auth (JWT)      │        │             │                    │
   │ • Users & Devices │        │             │                    │
   │ • Pairing         │   ┌────▼─────────────▼────────────────────▼──────┐
   │ • Friends         │   │          libp2p Overlay (Kademlia DHT)       │
   │ • Device Keys     │   └─────────────────────────────────────────────┘
   └───────────────────┘
         │                              ┌──────────────────┐
         ▼                              │   vinctum-ml     │ (optional)
   ┌───────────┐                        │  FastAPI + ONNX  │
   │  Redis 7  │                        │  /score /anomaly │
   │ Blacklist │                        │  /route /health  │
   │ + Pairing │                        └──────────────────┘
   └───────────┘

   ┌──────────────────────────────────────────────────────────────────────┐
   │                      PostgreSQL 16                                   │
   │   identity DB  │  discovery DB  │  routing DB  │  transfer DB       │
   └──────────────────────────────────────────────────────────────────────┘
```

Each gRPC service exposes Prometheus metrics on `grpcPort + 1000` (e.g., Identity at `:51051/metrics`). The gateway exposes metrics on `:8080/metrics`.

### Services

| Service | Port | Transport | Responsibilities |
|---------|------|-----------|-----------------|
| **Identity** | 50051 | gRPC | Users, JWT auth (HMAC-SHA256), email verification, devices, pairing, peer sessions, X25519 device keys, friends, notifications, admin queries |
| **Discovery** | 50052 | gRPC | Peer registry, Kademlia DHT bootstrap, signed peer announcements (Ed25519), peer streaming |
| **Routing** | 50053 | gRPC | Route computation, relay pool management, intelligence-aware path scoring, ML integration |
| **Transfer** | 50054 | gRPC | Chunk-based file transfer, transfer lifecycle, watch streams; co-hosts Relay service |
| **Relay** | — | gRPC | In-process with Transfer; multi-hop forwarding, TTL handling, rerouting, chunk replication |
| **Gateway** | 8080 | HTTP | REST-to-gRPC proxy, NDJSON event streams, CORS, security headers, admin panel API |

### Backing Stores

| Store | Version | Purpose |
|-------|---------|---------|
| **PostgreSQL** | 16 | Persistent storage (one logical DB per service, 16 migrations, auto-applied on boot) |
| **Redis** | 7 | JWT blacklist for token revocation + short-lived pairing codes |
| **Filesystem** | — | Encrypted chunk store (`VINCTUM_CHUNK_DIR`, default `./data/chunks`) |
| **SMTP** | — | Email verification delivery (optional, stub mode for dev) |

## Project Structure

```
vinctum-core/
├── cmd/                             # Service entry points (5 binaries)
│   ├── identity/                    #   main.go — bootstraps identity service
│   ├── discovery/                   #   main.go — bootstraps discovery service
│   ├── routing/                     #   main.go — bootstraps routing service
│   ├── transfer/                    #   main.go — co-hosts relay handler
│   └── gateway/                     #   main.go — HTTP server
├── services/                        # Business logic per service
│   ├── identity/                    #   handler/ + repository/ (sqlc-generated)
│   ├── discovery/                   #   handler/ + repository/ (sqlc-generated)
│   ├── routing/                     #   handler/ + repository/ (sqlc-generated)
│   ├── transfer/                    #   handler/ + repository/ + storage/ (chunk store)
│   ├── relay/                       #   handler/ (no DB — hosted inside transfer process)
│   └── gateway/                     #   handler/ (HTTP routes + NDJSON streams)
├── proto/                           # Protobuf definitions + generated Go stubs
│   ├── identity/v1/                 #   users, devices, auth, friends, admin
│   ├── discovery/v1/                #   peer registry, node announce
│   ├── routing/v1/                  #   route computation, relay management
│   ├── transfer/v1/                 #   file transfers, chunk upload/download
│   ├── relay/v1/                    #   inter-node chunk relay
│   └── gateway/v1/                  #   gateway-specific messages
├── internal/                        # Private packages (not importable)
│   ├── auth/                        #   JWT issuer/validator, Redis blacklist, pairing codes
│   ├── encryption/                  #   AES-256-GCM helpers
│   ├── intelligence/                #   Node scoring, anomaly detection, ML adapter
│   ├── migrator/                    #   Embedded SQL migration runner
│   ├── p2p/                         #   libp2p node (DHT, mDNS)
│   └── relay/                       #   Relay client, peer pool, circuit breaker, rerouter, replicator
├── pkg/                             # Shared public packages
│   ├── config/                      #   Viper config loader (YAML + VINCTUM_* env overlay)
│   ├── crypto/                      #   X25519 ECDH, HKDF-SHA256, AES-256-GCM, Ed25519
│   ├── grpcutil/                    #   TLS/mTLS credential loading
│   ├── logger/                      #   Zerolog structured logging
│   ├── mailer/                      #   SMTP verification mailer
│   └── middleware/                  #   gRPC interceptors: auth, rate limit, metrics, audit
├── scripts/migrations/              # SQL schema files (016 migrations, embedded, auto-applied)
├── config/                          # YAML configs (config.dev.yaml)
├── deployments/docker/              # Dockerfiles (per service) + docker-compose.yml
├── docs/                            # Architecture docs, threat model, ADRs
│   ├── threat_model.md              #   STRIDE-based security analysis
│   └── adr/                         #   7 Architecture Decision Records
└── .github/workflows/               # CI pipeline (lint → test → build → docker push)
```

## End-to-End Encryption

Vinctum enforces a strict zero-knowledge architecture: the server never holds plaintext or symmetric keys. Encryption is performed entirely on the client side.

### Key Exchange Protocol

```
Sender                          Identity Service                       Receiver
  │                                    │                                   │
  │                                    │◄── UploadDeviceKey(X25519 pub) ───┤
  │                                    │    (registered once per device)    │
  │                                    │                                   │
  │── GetDeviceKey(receiver_device) ──►│                                   │
  │◄── receiver_static_pub ───────────│                                   │
  │                                    │                                   │
  │  [Generate ephemeral X25519 pair]  │                                   │
  │  [ECDH: ephemeral_priv × recv_pub] │                                   │
  │  [HKDF-SHA256 → 32-byte AES key]  │                                   │
  │    salt = ephemeral_pub || recv_pub│                                   │
  │    info = "vinctum-transfer-v1:<id>"                                   │
  │                                    │                                   │
  │── InitiateTransfer ───────────────►│                                   │
  │   (sender_ephemeral_pubkey stored) │                                   │
  │── UploadChunk(ciphertext) ────────►│                                   │
  │                                    │        ── relay / store ─────────►│
  │                                    │                                   │
  │                                    │◄── GetTransferStatus ─────────────┤
  │                                    │──► (sender_ephemeral_pubkey) ─────┤
  │                                    │                                   │
  │                                    │    [ECDH: recv_priv × eph_pub]    │
  │                                    │    [HKDF-SHA256 → same AES key]   │
  │                                    │    [Decrypt chunks]               │
```

| Component | Algorithm | Details |
|-----------|-----------|---------|
| Static device keys | X25519 | 32-byte public key registered with Identity per device |
| Ephemeral key | X25519 | Fresh keypair generated per transfer by sender |
| Key agreement | ECDH | `ephemeral_private * receiver_static_public` |
| Key derivation | HKDF-SHA256 | Salt: `ephemeralPub \|\| receiverStaticPub`, Info: `vinctum-transfer-v1:<transfer_id>` |
| Chunk encryption | AES-256-GCM | 32-byte key, 12-byte random nonce prepended to ciphertext |
| Integrity | SHA-256 | Mandatory content hash on every chunk, verified at every relay hop |
| MITM binding | Content hash | Must cover ephemeral pubkey client-side for binding |

> **Design constraint:** The server only stores ciphertext. This is enforced architecturally — there is no API to submit a plaintext chunk or retrieve a symmetric key from the server. Re-introducing server-side key escrow is explicitly prohibited.

## Chunk Transfer Protocol

```
Sender                  Transfer Service         Relay (hop 1)         Relay (hop N)          Receiver
  │                           │                       │                     │                      │
  │── InitiateTransfer ──────►│                       │                     │                      │
  │◄── transfer_id ───────────│                       │                     │                      │
  │                           │                       │                     │                      │
  │── UploadChunk ───────────►│                       │                     │                      │
  │   (AES-256-GCM ciphertext │── RelayChunk ────────►│                     │                      │
  │    + SHA-256 hash)        │   (TTL, hops[])       │── RelayChunk ──────►│        ...           │
  │                           │                       │   (TTL-1, hops[1:]) │── store at dest ────►│
  │                           │                       │                     │                      │
  │                           │                       │   ┌─ on failure ──┐ │                      │
  │                           │                       │   │ circuit opens │ │                      │
  │                           │                       │   │ reroute via   │ │                      │
  │                           │                       │   │ routing svc   │ │                      │
  │                           │                       │   └───────────────┘ │                      │
  │                           │                       │                     │                      │
  │── GET /transfers/watch ──►│ ─────────── NDJSON event stream ──────────────────────────────────►│
```

**Transfer modes:**

| Mode | Description |
|------|-------------|
| **Relay** | Chunks traverse relay nodes hop-by-hop to the destination |
| **P2P Direct** | Devices connect directly via libp2p (Kademlia DHT, NAT hole punching) |
| **P2P Relayed** | P2P connection via libp2p circuit relay when direct connection fails |

**Guarantees:**
- SHA-256 integrity hash verified at every hop — tampered chunks are rejected
- TTL decrements at each hop, preventing infinite relay loops
- Replication factor > 1 stores chunks across multiple relay nodes
- NDJSON streaming (`/api/v1/transfers/watch`) for real-time transfer state updates

## Relay & Rerouting

The relay subsystem (`internal/relay/`) provides fault-tolerant chunk forwarding across the mesh.

```
                    ┌──────────────────────────────────────────────────┐
                    │              internal/relay/                      │
                    │                                                   │
                    │  ┌─────────────┐    ┌────────────────────────┐   │
                    │  │  PeerPool   │    │    CircuitBreaker      │   │
                    │  │─────────────│    │────────────────────────│   │
                    │  │ • gRPC conn │    │ • Per-node state       │   │
                    │  │   cache     │    │   machine              │   │
                    │  │ • Lazy dial │    │ • Closed → Open →     │   │
                    │  │ • Eviction  │    │   HalfOpen → Closed   │   │
                    │  └──────┬──────┘    │ • Configurable max    │   │
                    │         │           │   failures + cooldown │   │
                    │         ▼           └───────────┬────────────┘   │
                    │  ┌─────────────┐                │               │
                    │  │   Client    │◄───────────────┘               │
                    │  │─────────────│    ┌────────────────────────┐   │
                    │  │ • RelayChunk│───►│      Rerouter          │   │
                    │  │ • SendChunk │    │────────────────────────│   │
                    │  └─────────────┘    │ • Calls Routing svc   │   │
                    │                     │ • Excludes failed node │   │
                    │  ┌─────────────┐    │ • Max 10 hops         │   │
                    │  │ Replicator  │    └────────────────────────┘   │
                    │  │─────────────│                                  │
                    │  │ • Background│                                  │
                    │  │   chunk     │                                  │
                    │  │   copies    │                                  │
                    │  └─────────────┘                                  │
                    └──────────────────────────────────────────────────┘
```

| Component | Responsibility |
|-----------|---------------|
| **PeerPool** | Cached gRPC connections per node, lazy dial, thread-safe with double-checked locking |
| **CircuitBreaker** | Per-node state machine (Closed/Open/HalfOpen); prevents hammering failed nodes |
| **Rerouter** | Queries routing service for alternative paths, excluding the failed node |
| **Replicator** | Background goroutine that copies chunks to additional nodes for redundancy |
| **Client** | Orchestrates relay calls with circuit breaker checks and automatic rerouting on failure |

## Intelligent Routing

The routing service uses a scoring engine (`internal/intelligence/`) to select optimal relay paths. An optional external ML service (vinctum-ml) provides advanced predictions.

### Node Scoring

Each node is scored based on observed behavior:

| Factor | Weight | Source |
|--------|--------|--------|
| **Uptime** | 40% | Success/failure ratio from relay events |
| **Latency** | 30% | Average, min, max, P95 from ping/relay timing |
| **Throughput** | 15% | Bytes transferred per time window |
| **Stability** | 15% | Variance in latency and failure rate |

Confidence scales with event count — a node needs ~50 events for full confidence. Route scores are computed multiplicatively along the path (each hop's score multiplied together).

### Anomaly Detection

The intelligence layer detects four anomaly types:

| Type | Trigger |
|------|---------|
| `LatencySpike` | Sudden latency increase beyond Z-score threshold |
| `HighFailureRate` | Failure percentage exceeds configured limit |
| `TrafficSpike` | Unusual traffic volume on a node |
| `NodeUnresponsive` | Complete unavailability (no successful events) |

**Local fallback:** When the external ML service is unavailable, the system falls back to local Z-score-based anomaly detection — the platform never depends on an external service for core functionality.

### ML Integration (Optional)

The vinctum-ml service (separate repo, FastAPI + ONNX) provides four endpoints:

| Endpoint | Purpose |
|----------|---------|
| `POST /score` | ML-enhanced node scoring based on historical patterns |
| `POST /anomaly` | Anomaly detection with trained models |
| `POST /route` | Route optimization using learned traffic patterns |
| `GET /health` | Service health check |

Communication uses API key authentication (`X-API-Key` header). The gateway proxies these endpoints under `/api/v1/ml/*`.

## Getting Started

### Prerequisites

- Go 1.25+
- Docker & Docker Compose
- [buf](https://buf.build/docs/installation) (protobuf generation)
- [sqlc](https://sqlc.dev/) (SQL code generation)

### Quick Start

```bash
# Clone
git clone https://github.com/saitddundar/vinctum-core.git
cd vinctum-core

# Generate code from proto and SQL definitions
make generate

# Start full stack (Postgres + Redis + all 5 services + gateway)
make docker-up

# Or run services individually (requires Postgres + Redis running)
docker compose -f deployments/docker/docker-compose.yml up postgres redis -d
make run-identity       # terminal 1
make run-discovery      # terminal 2
make run-routing        # terminal 3
make run-transfer       # terminal 4
make run-gateway        # terminal 5
```

### Verify Setup

```bash
# Health check
curl http://localhost:8080/health

# Register a user
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","email":"alice@example.com","password":"strongpassword"}'

# Login (after email verification)
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","password":"strongpassword"}'
```

### Docker Compose Stack

```bash
make docker-up    # Builds and starts everything
make docker-down  # Tears down all containers
```

The `docker-compose.yml` defines:

| Container | Port | Notes |
|-----------|------|-------|
| postgres | 5432 | PostgreSQL 16 with health checks |
| redis | 6379 | Redis 7 with health checks |
| identity | 50051 | Depends on postgres + redis |
| discovery | 50052 | Depends on postgres |
| routing | 50053 | Depends on postgres |
| transfer | 50054 | Depends on postgres |
| gateway | 8080 | Depends on all services; CORS configured for dev |

### Local Development Tips

- Run `make generate` after editing any `.proto` or `.sql` file
- Use `make tidy` to sync Go dependencies after adding a package
- Migrations in `scripts/migrations/` are embedded and auto-applied on each service boot — no manual migration step needed
- SMTP is optional; without it, email verification works in stub mode for local dev

## Configuration

All services share a Viper-based config system with YAML files and environment variable overrides (`VINCTUM_*` prefix, `_` separator for nested keys).

### Development Config (`config/config.dev.yaml`)

```yaml
service:
  name: identity-service
  environment: development
  version: 0.1.0
  log_level: debug

grpc:
  host: 0.0.0.0
  port: 50051
  tls_enabled: false          # set true + provide cert paths for mTLS

auth:
  jwt_secret: "change-me-in-production"
  jwt_expiry: 24h             # access token lifetime
  refresh_expiry: 168h        # refresh token lifetime (7 days)
  bcrypt_cost: 12

database:
  driver: postgres
  dsn: postgres://vinctum:vinctum@localhost:5432/vinctum_identity?sslmode=disable
  max_open_conns: 25
  max_idle_conns: 10
  conn_max_lifetime: 5m

p2p:
  listen_addresses:
    - /ip4/0.0.0.0/tcp/4001
    - /ip4/0.0.0.0/udp/4001/quic-v1
  enable_dht: true
  enable_relay: true
```

### Environment Variables

| Variable | Default | Used by |
|----------|---------|---------|
| `VINCTUM_SERVICE_NAME` | — | all |
| `VINCTUM_GRPC_PORT` | 50051 | gRPC services |
| `VINCTUM_DATABASE_DSN` | (see config) | all except gateway |
| `VINCTUM_AUTH_JWT_SECRET` | change-me-in-prod | identity, middleware |
| `VINCTUM_REDIS_ADDR` | localhost:6379 | identity |
| `VINCTUM_ROUTING_ADDR` | localhost:50053 | transfer |
| `VINCTUM_CHUNK_DIR` | ./data/chunks | transfer |
| `VINCTUM_GATEWAY_*_ADDR` | localhost:5005x | gateway |
| `VINCTUM_GATEWAY_HTTP_PORT` | 8080 | gateway |
| `VINCTUM_ML_API_URL` | — | routing |
| `VINCTUM_ML_API_KEY` | — | routing, gateway |
| `VINCTUM_GRPC_TLS_ENABLED` | false | all gRPC |
| `VINCTUM_GRPC_CERT_FILE` | — | all gRPC |
| `VINCTUM_GRPC_KEY_FILE` | — | all gRPC |
| `VINCTUM_GRPC_CA_FILE` | — | all gRPC |

## Security

### Authentication & Authorization

| Layer | Mechanism |
|-------|-----------|
| **Password** | bcrypt (cost 12, configurable) |
| **Access tokens** | JWT HMAC-SHA256, 24h expiry |
| **Refresh tokens** | JWT HMAC-SHA256, 7-day expiry, rotation with old-token blacklisting |
| **Token revocation** | Redis-backed blacklist, checked on every authenticated request |
| **Email verification** | Mandatory before login; SMTP delivery via `pkg/mailer` |
| **gRPC auth** | Interceptors on all services; method-level bypass only for public endpoints |
| **Admin access** | Email-based check at gateway level |

### Encryption & Integrity

| Layer | Mechanism |
|-------|-----------|
| **Chunk encryption** | AES-256-GCM (client-side, server never sees plaintext) |
| **Key exchange** | X25519 ECDH + HKDF-SHA256 (per-transfer ephemeral keys) |
| **Chunk integrity** | SHA-256 hash mandatory on every chunk, verified at every relay hop |
| **Peer announcements** | Ed25519 signatures over `(node_id \|\| addrs \|\| public_key)` prevent spoofing |
| **Transport** | Optional mTLS between gRPC services (`pkg/grpcutil`) |

### Access Control

| Feature | Control |
|---------|---------|
| **Device pairing** | 6-char codes (crypto/rand), Redis-stored, 5-min TTL, approver accept/reject |
| **Device visibility** | `is_public` flag — public devices visible to friends, private to owner only |
| **Friend devices** | `ListFriendDevices` returns only `is_public = TRUE AND is_approved = TRUE AND revoked_at IS NULL` for accepted friendships |
| **SQL injection** | Eliminated — all queries generated by sqlc with parameterized statements |
| **Gateway** | Security headers, CORS whitelist, body size limits |
| **Rate limiting** | Token-bucket per peer, applied via gRPC interceptor |

### Threat Model

A full STRIDE-based security analysis is maintained at [`docs/threat_model.md`](docs/threat_model.md), covering:

| Category | Trust Boundaries |
|----------|-----------------|
| **Spoofing** | JWT forgery, P2P node impersonation |
| **Tampering** | In-transit chunk modification, chunk hash bypass |
| **Repudiation** | Audit logging of all gRPC calls |
| **Information Disclosure** | Peer list enumeration, key material leakage |
| **Denial of Service** | gRPC flooding, fake peer registrations |
| **Elevation of Privilege** | Unauthorized relay access, JWT claim manipulation |

## Observability

### Prometheus Metrics

Every service exports metrics on a dedicated HTTP port (`grpcPort + 1000`):

| Service | Metrics Port | Endpoint |
|---------|-------------|----------|
| Identity | 51051 | `/metrics` |
| Discovery | 52052 | `/metrics` |
| Routing | 53053 | `/metrics` |
| Transfer | 54054 | `/metrics` |
| Gateway | 8080 | `/metrics` |

### Audit Logging

The audit middleware (`pkg/middleware/audit.go`) records every gRPC call to the `audit_logs` table:

| Field | Description |
|-------|-------------|
| `method` | Full gRPC method path |
| `user_id` | Authenticated user (if any) |
| `code` | gRPC status code |
| `peer_addr` | Client IP address |
| `duration_ms` | Request processing time |
| `created_at` | Timestamp |

### Structured Logging

All services use zerolog for JSON-structured logging with consistent field patterns across the platform.

## HTTP API Reference

All routes are exposed by the gateway on `:8080`. JWT bearer tokens are required except where noted.

### Auth

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/auth/register` | Public | Register new user |
| `POST` | `/api/v1/auth/login` | Public | Login (requires verified email) |
| `POST` | `/api/v1/auth/refresh` | Public | Refresh access token |
| `POST` | `/api/v1/auth/validate` | Public | Validate JWT token |
| `POST` | `/api/v1/auth/verify` | Public | Verify email with token |
| `POST` | `/api/v1/auth/resend-verification` | Public | Resend verification email |

### Devices

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/devices` | Register a new device |
| `GET` | `/api/v1/devices` | List own devices |
| `GET` | `/api/v1/devices/{id}` | Get device details |
| `DELETE` | `/api/v1/devices/{id}` | Revoke a device |
| `PUT` | `/api/v1/devices/{id}/activity` | Device heartbeat |
| `PUT` | `/api/v1/devices/{id}/visibility` | Toggle public/private |

### Device Pairing

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/devices/pairing/generate` | Generate 6-char code (5 min TTL) |
| `POST` | `/api/v1/devices/pairing/redeem` | Redeem code, create pending device |
| `POST` | `/api/v1/devices/pairing/approve` | Accept or reject pairing |

### Sessions

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/sessions` | Create peer session |
| `GET` | `/api/v1/sessions` | List sessions |
| `POST` | `/api/v1/sessions/{id}/close` | Close session |
| `POST` | `/api/v1/sessions/{id}/join` | Join session |
| `POST` | `/api/v1/sessions/{id}/leave` | Leave session |
| `GET` | `/api/v1/sessions/{id}/devices` | List session devices |

### Device Keys (E2E)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/devices/{id}/key` | Upload X25519 public key (32 bytes) |
| `GET` | `/api/v1/devices/{id}/key` | Get device public key |
| `GET` | `/api/v1/sessions/{id}/keys` | Get all device keys in a session |
| `GET` | `/api/v1/nodes/{id}/key` | Cross-user key lookup by node ID |

### Friends

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/friends/request` | Send friend request |
| `POST` | `/api/v1/friends/respond` | Accept or reject request |
| `GET` | `/api/v1/friends` | List accepted friends |
| `GET` | `/api/v1/friends/requests` | List pending incoming requests |
| `DELETE` | `/api/v1/friends/{id}` | Remove friend |
| `GET` | `/api/v1/friends/{userId}/devices` | List friend's public devices |
| `GET` | `/api/v1/users/search` | Search users by username |

### Transfers

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/transfers` | Initiate transfer (ciphertext only) |
| `GET` | `/api/v1/transfers/{id}` | Get transfer status |
| `GET` | `/api/v1/transfers/node/{nodeId}` | List transfers for a node |
| `POST` | `/api/v1/transfers/{id}/cancel` | Cancel transfer |
| `POST` | `/api/v1/transfers/{id}/chunks` | Upload encrypted chunk |
| `GET` | `/api/v1/transfers/{id}/chunks` | Download encrypted chunks |
| `GET` | `/api/v1/transfers/watch` | NDJSON event stream |

### Routing & Intelligence

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/routes/find` | Compute route between nodes |
| `GET` | `/api/v1/routes/table/{nodeId}` | Get routing table |
| `GET` | `/api/v1/relays` | List relay nodes |
| `GET` | `/api/v1/ml/health` | ML service health check |
| `POST` | `/api/v1/ml/score` | ML node scoring |
| `POST` | `/api/v1/ml/anomaly` | ML anomaly detection |
| `POST` | `/api/v1/ml/route` | ML route optimization |

### Admin (restricted)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/stats` | Platform statistics (public) |
| `GET` | `/api/v1/admin/users` | List all users |
| `GET` | `/api/v1/admin/devices` | List all devices |
| `GET` | `/api/v1/admin/transfers` | List all transfers |
| `GET` | `/api/v1/admin/audit-logs` | List audit log entries |
| `GET` | `/api/v1/admin/services` | Service health status |

### Other

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Gateway health check (public) |
| `GET` | `/services` | Service health overview (public) |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/api/v1/notifications/count` | Pending notification count |

## Testing

```bash
make test          # go test ./... -v -race -count=1
make test-cover    # generates coverage.out + HTML report
make lint          # golangci-lint run ./...
```

Handler tests use **fake implementations** of the sqlc-generated `Querier` interface (`fakeQuerier`), so they run without a database. Each service's repository layer exposes a `Querier` interface that the handler depends on — tests inject fakes, production code uses the real sqlc implementation.

The CI pipeline runs tests with real PostgreSQL 16 + Redis 7 service containers for integration-level coverage.

## CI/CD

GitHub Actions pipeline (`.github/workflows/ci.yml`):

```
┌────────┐     ┌────────┐     ┌──────────────┐     ┌──────────────────┐
│  Lint  │────►│  Test  │────►│    Build     │────►│  Docker Push     │
│        │     │        │     │  (5×matrix)  │     │  (4×matrix)      │
│ golangci│     │ Pg + Redis│   │  identity    │     │  GHCR + SHA tag  │
│ -lint  │     │ race+cover│   │  discovery   │     │  (main only)     │
└────────┘     └────────┘     │  routing     │     └──────────────────┘
                              │  transfer    │
                              │  gateway     │
                              └──────────────┘
```

| Stage | Details |
|-------|---------|
| **Lint** | golangci-lint with 5-minute timeout |
| **Test** | PostgreSQL 16 + Redis 7 service containers, race detector, coverage artifact upload |
| **Build** | Matrix build across 5 services, output to `bin/` |
| **Docker** | Multi-stage build & push to GHCR on `main` branch (latest + git SHA tags) |

## Makefile Commands

| Target | Description |
|--------|-------------|
| `make generate` | Run both proto and sqlc code generation |
| `make generate-proto` | Generate Go stubs from `.proto` files via buf |
| `make generate-sql` | Generate Go code from SQL queries via sqlc |
| `make build` | Build all service binaries to `bin/` |
| `make run-identity` | Run identity service locally |
| `make run-discovery` | Run discovery service locally |
| `make run-routing` | Run routing service locally |
| `make run-transfer` | Run transfer service locally |
| `make run-gateway` | Run gateway service locally |
| `make test` | Run all tests with race detector |
| `make test-cover` | Tests + HTML coverage report |
| `make lint` | Run golangci-lint |
| `make tidy` | Sync Go module dependencies |
| `make docker-up` | Build & start all containers |
| `make docker-down` | Stop all containers |
| `make clean` | Remove build artifacts |

## Architecture Decision Records

7 ADRs document key architectural choices in [`docs/adr/`](docs/adr/):

| ADR | Decision | Rationale |
|-----|----------|-----------|
| **001** | Microservices over monolith | Independent scaling, fault isolation, bounded contexts |
| **002** | gRPC over REST for internal comms | Binary serialization, streaming, type-safe contracts |
| **003** | libp2p for P2P networking | Battle-tested DHT, NAT traversal, Ed25519 peer identity |
| **004** | sqlc over ORM | Compile-time safety, no reflection, generated `Querier` interface |
| **005** | JWT with HMAC-SHA256 | Stateless verification, refresh rotation, Redis blacklist revocation |
| **006** | Monorepo over polyrepo | Atomic cross-service changes, single CI pipeline |
| **007** | AES-256-GCM for E2E encryption | Client-side only, 32-byte key from ECDH+HKDF, server never sees plaintext |

## Development Status

### Completed

| Area | Deliverables |
|------|-------------|
| **Core Services** | Identity, Discovery, Routing, Transfer, Relay, Gateway — all operational |
| **Authentication** | JWT auth, refresh rotation, Redis blacklist, bcrypt passwords, email verification |
| **Encryption** | AES-256-GCM E2E, X25519 ECDH key exchange, HKDF-SHA256 key derivation |
| **Device Management** | Registration, pairing (6-char codes), approval flow, visibility (public/private) |
| **Social** | Friend system (request/accept/reject/remove), friend device discovery, user search |
| **Transfer Engine** | Chunk-based transfer, multi-hop relay, TTL, SHA-256 integrity, NDJSON streaming |
| **Relay Mesh** | Connection pooling, per-node circuit breaker, automatic rerouting, chunk replication |
| **Intelligence** | Node scoring (uptime/latency/throughput/stability), Z-score anomaly detection, ML adapter |
| **P2P Layer** | libp2p with Kademlia DHT, signed Ed25519 peer announcements |
| **Observability** | Prometheus metrics on all services, audit logging, structured JSON logging |
| **Security** | STRIDE threat model, mTLS support, rate limiting, signed peer announcements |
| **Infrastructure** | Docker Compose stack, GitHub Actions CI/CD, multi-stage Docker builds, GHCR publishing |
| **Admin** | Admin API with email-based access control, platform stats, user/device/transfer/audit management |
| **Web Client** | React SPA with dashboard, device management, transfers, friends, admin panel ([vinctum-web](https://github.com/saitddundar/vinctum-web)) |

### Planned

| Feature | Description |
|---------|-------------|
| **P2P Direct Transfer** | Direct device-to-device file transfer over libp2p without relay |
| **Mobile Client** | iOS / Android companion app |
| **File Previews** | Thumbnail generation for images and documents |
| **Transfer History** | Persistent transfer history with search and filtering |

## Tech Stack

| Category | Technology |
|----------|-----------|
| Language | Go 1.25 |
| RPC | gRPC + Protobuf (buf.build toolchain) |
| P2P | go-libp2p (Kademlia DHT, mDNS) |
| Auth | JWT (HMAC-SHA256) + bcrypt + Redis-backed blacklist |
| Database | PostgreSQL 16 via pgx/v5 |
| Query Generation | sqlc (type-safe, no ORM) |
| Cache | Redis 7 (go-redis) |
| Chunk Encryption | AES-256-GCM (client-side E2E) |
| Key Exchange | X25519 ECDH + HKDF-SHA256 |
| Peer Identity | Ed25519 signed announcements |
| Transport Security | Optional mTLS via `pkg/grpcutil` |
| Metrics | Prometheus (per-service `/metrics`) |
| Config | Viper (YAML + `VINCTUM_*` env overlay) |
| Logging | Zerolog (structured JSON) |
| Mail | SMTP via `pkg/mailer` |
| ML | FastAPI + ONNX (optional external service) |
| CI/CD | GitHub Actions |
| Containers | Docker + GHCR multi-stage builds |
| Frontend | React 19 + TypeScript + Vite ([vinctum-web](https://github.com/saitddundar/vinctum-web)) |

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | C4 diagrams, service responsibilities, deployment topology |
| [Threat Model](docs/threat_model.md) | STRIDE-based security analysis with risk matrix |
| [ADRs](docs/adr/) | 7 Architecture Decision Records |
| [Project Plan](docs/Vinctum_14_Haftalik_Plan.md) | 14-week development roadmap |

## License

MIT License — see [LICENSE](LICENSE) for details.
