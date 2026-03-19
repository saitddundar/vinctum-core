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

| Layer      | Technology         |
|------------|--------------------|
| Language   | Go 1.22+           |
| RPC        | gRPC + Protobuf    |
| P2P        | go-libp2p (DHT)    |
| Auth       | JWT + mTLS         |
| VPN        | WireGuard (opt.)   |
| Config     | Viper              |
| Logging    | Zerolog            |

## Project Structure

```
vinctum-core/
├── cmd/           # Service entry points
├── services/      # Microservice implementations
│   ├── identity/
│   ├── discovery/
│   ├── routing/
│   ├── transfer/
│   └── gateway/
├── proto/         # Protobuf schemas
├── pkg/           # Shared packages (config, logger, crypto)
├── internal/      # Internal packages
├── deployments/   # Docker & Kubernetes manifests
├── scripts/
└── docs/adr/      # Architecture Decision Records
```

## Getting Started

```bash
go mod download
buf generate          # requires buf CLI
go run ./cmd/identity/...
```

## License

MIT
