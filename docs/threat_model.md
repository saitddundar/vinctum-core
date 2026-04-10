# Vinctum-Core Security Threat Model

**Version:** 1.0  
**Date:** 27 March 2026  
**Author:** Vinctum Core Team  
**Methodology:** STRIDE + Risk Matrix

---

## 1. System Overview

Vinctum is a microservice-based platform providing end-to-end encrypted P2P communication.

### 1.1 Components

| Component | Description | Port |
|-----------|-------------|------|
| Identity Service | User registration, JWT authentication | 50051 |
| Discovery Service | P2P node discovery, peer registry | 50052 |
| Routing Service | Message routing, relay selection | 50053 |
| Transfer Service | File/data transfer, chunk management | 50054 |
| PostgreSQL | Persistent data store | 5432 |
| Redis | Token blacklist, cache | 6379 |
| libp2p Network | P2P inter-node communication | 4001 |

### 1.2 Data Flow

```
User → [gRPC + TLS] → Identity Service → [JWT Token]
  ↓
Discovery Service → [Peer Registry] → libp2p DHT
  ↓
Routing Service → [Shortest Path / Relay] → Transfer Service
  ↓
Transfer Service → [E2E Encrypted Chunks] → Target Peer
```

---

## 2. Trust Boundaries

```
┌─────────────────────────────────────────────────────────────────┐
│                     INTERNET (Untrusted)                        │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │         TB-1: gRPC API Boundary (TLS + JWT)              │   │
│  │                                                          │   │
│  │   ┌─────────────────────────────────────────────────┐    │   │
│  │   │     TB-2: Inter-service Communication           │    │   │
│  │   │                                                 │    │   │
│  │   │   ┌──────────────────────────────────────┐      │    │   │
│  │   │   │  TB-3: Data Layer                    │      │    │   │
│  │   │   │  (PostgreSQL / Redis)                │      │    │   │
│  │   │   └──────────────────────────────────────┘      │    │   │
│  │   └─────────────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │         TB-4: P2P Network Boundary (libp2p)              │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

---

## 3. STRIDE Analysis

### 3.1 Spoofing (Identity Forgery)

| ID | Threat | Target | Severity | Likelihood | Risk | Existing Control | Recommended Action |
|----|--------|--------|----------|------------|------|-----------------|-------------------|
| S-01 | Stolen JWT token used for impersonation | Identity Service | High | Medium | **High** | HMAC-SHA256 signed JWT, token blacklist | Refresh token rotation, IP binding |
| S-02 | Forged P2P node_id | Discovery Service | High | Medium | **High** | Ed25519 public key verification | Peer identity handshake protocol |
| S-03 | gRPC metadata spoofing | All services | Medium | Low | **Medium** | JWT validation interceptor | Add mTLS |

### 3.2 Tampering (Data Modification)

| ID | Threat | Target | Severity | Likelihood | Risk | Existing Control | Recommended Action |
|----|--------|--------|----------|------------|------|-----------------|-------------------|
| T-01 | SQL injection | Identity/Discovery repos | High | Low | **Medium** | sqlc parameterized queries | Input validation layer |
| T-02 | Peer address manipulation | Discovery Service | Medium | Medium | **Medium** | Database upsert | Address verification, signed announcements |
| T-03 | In-transit data modification | Transfer Service | Critical | Medium | **Critical** | Client-side AES-256-GCM (AEAD) — server holds only ciphertext; SHA-256 transport hash on chunks | Per-chunk receiver-side AAD binding |

### 3.3 Repudiation (Action Denial)

| ID | Threat | Target | Severity | Likelihood | Risk | Existing Control | Recommended Action |
|----|--------|--------|----------|------------|------|-----------------|-------------------|
| R-01 | User action repudiation | Identity Service | Medium | Medium | **Medium** | zerolog structured logging | Audit log table, signed log chain |
| R-02 | P2P message repudiation | Routing/Transfer | Medium | Medium | **Medium** | None yet | Digitally signed message envelopes |

### 3.4 Information Disclosure

| ID | Threat | Target | Severity | Likelihood | Risk | Existing Control | Recommended Action |
|----|--------|--------|----------|------------|------|-----------------|-------------------|
| I-01 | JWT secret leakage | Identity Service | Critical | Low | **High** | ENV variable | Vault/KMS integration |
| I-02 | Database DSN logged in plaintext | All services | High | Low | **Medium** | Config file | Secrets masking |
| I-03 | Full peer list disclosure | Discovery Service | Medium | High | **High** | Auth interceptor | Rate limiting, pagination |
| I-04 | Password hash algorithm leakage | Identity Service | Low | Low | **Low** | bcrypt (cost=12) | Sufficient |

### 3.5 Denial of Service

| ID | Threat | Target | Severity | Likelihood | Risk | Existing Control | Recommended Action |
|----|--------|--------|----------|------------|------|-----------------|-------------------|
| D-01 | gRPC flood attack | All services | High | High | **Critical** | None | Rate limiting interceptor |
| D-02 | Fake peer registrations flooding registry | Discovery Service | High | Medium | **High** | None | Registration limit, proof-of-work |
| D-03 | Oversized file transfer attack | Transfer Service | Medium | Medium | **Medium** | max_recv_msg_size (4MB) | Chunk size limit, quota system |
| D-04 | bcrypt CPU exhaustion via login spam | Identity Service | Medium | Medium | **Medium** | None | Login rate limiting |

### 3.6 Elevation of Privilege

| ID | Threat | Target | Severity | Likelihood | Risk | Existing Control | Recommended Action |
|----|--------|--------|----------|------------|------|-----------------|-------------------|
| E-01 | JWT claim manipulation | Identity Service | Critical | Low | **High** | HMAC signature verification | Claim content validation, role-based claims |
| E-02 | Public method bypass | Middleware | High | Low | **Medium** | Whitelist (publicMethods map) | Deny-by-default policy |
| E-03 | Unauthorized access via relay node | Routing/Discovery | High | Medium | **High** | None yet | Relay authentication, ACL |

---

## 4. Risk Matrix Summary

|  | Low Likelihood | Medium Likelihood | High Likelihood |
|---|---|---|---|
| **Critical Severity** | T-03, E-01 | — | D-01 |
| **High Severity** | T-01, I-01 | S-01, S-02, D-02, E-03 | I-03 |
| **Medium Severity** | S-03, I-02 | R-01, R-02, T-02, D-03, D-04 | — |
| **Low Severity** | I-04 | — | — |

---

## 5. Current Security Controls

### Implemented
- **JWT Authentication** — Access/Refresh token pair, HMAC-SHA256 signed
- **Token Blacklist** — Redis-based, invalidates tokens after logout
- **Password Hashing** — bcrypt with cost=12
- **gRPC Auth Interceptor** — Unary + streaming, all services including Discovery
- **Rate Limiting** — Token-bucket per-peer rate limiting on all gRPC services
- **E2E Encryption** — Client-side AES-256-GCM; the server stores only opaque ciphertext and rejects any request carrying an encryption key. SHA-256 chunk hashes verify transport integrity over the ciphertext.
- **mTLS** — TLS 1.3 mutual auth via `pkg/grpcutil`, config-driven
- **Prometheus Metrics** — gRPC request count, latency, active requests on all services
- **Network Intelligence** — Z-score anomaly detection, auto-excludes compromised nodes from routing
- **Parameterized SQL** — sqlc compile-time type-safe queries
- **Structured Logging** — zerolog with JSON output

### Remaining
- Input validation layer
- Audit logging
- Secrets management (Vault/KMS)
- Refresh token rotation
- Signed peer announcements

---

## 6. Prioritized Action Plan

| Priority | Action | Target | Related Threat |
|----------|--------|--------|---------------|
| P0 | ~~gRPC rate limiting interceptor~~ | All services | D-01, D-04 | **DONE** |
| P0 | ~~E2E encryption (AES-256-GCM, client-side)~~ | Transfer Service | T-03 | **DONE** |
| P1 | ~~mTLS for inter-service communication~~ | All services | S-03, E-02 | **DONE** (pkg/grpcutil) |
| P1 | Refresh token rotation | Identity Service | S-01 |
| P1 | Signed peer announcements | Discovery Service | S-02, T-02 |
| P2 | Audit log table + middleware | Identity Service | R-01 |
| P2 | Input validation layer | All handlers | T-01 |
| P2 | Secrets vault integration | Deployment | I-01, I-02 |
| P3 | Relay ACL system | Routing Service | E-03 |
| P3 | Peer registration throttling | Discovery Service | D-02 |

---

## 7. Attack Surface Map

| Entry Point | Protocol | Auth | Exposed Data |
|------------|----------|------|-------------|
| Identity gRPC (50051) | gRPC + TLS | JWT (public: Register, Login, Refresh) | User info, tokens |
| Discovery gRPC (50052) | gRPC | JWT (public: FindPeers, GetNodeInfo) | Peer addresses, public keys |
| Routing gRPC (50053) | gRPC | JWT | Routing tables |
| Transfer gRPC (50054) | gRPC | JWT | Encrypted data chunks |
| libp2p (4001/tcp+udp) | TCP/QUIC | Peer ID + Key | DHT records, peer info |
| PostgreSQL (5432) | TCP | Password | All persistent data |
| Redis (6379) | TCP | None (should be added) | Token blacklist |

---

## 8. Conclusion

The current security controls are **adequate at a foundational level** but the following must be completed **before production deployment**:

1. ~~**Rate limiting**~~ — **Implemented.** Token-bucket rate limiting on all gRPC services.
2. ~~**E2E encryption**~~ — **Implemented.** True end-to-end: AES-256-GCM performed on the client; the Transfer service stores only opaque ciphertext and rejects requests carrying an encryption key.
3. ~~**mTLS**~~ — **Implemented.** TLS 1.3 with mutual certificate verification via `pkg/grpcutil`.
4. ~~**Discovery auth**~~ — **Implemented.** JWT auth interceptor added; FindPeers/GetNodeInfo remain public.
5. **Prometheus observability** — **Implemented.** gRPC metrics interceptors + `/metrics` endpoints on all services.
6. **Network intelligence** — **Implemented.** Anomaly detection auto-excludes compromised nodes from routes.
