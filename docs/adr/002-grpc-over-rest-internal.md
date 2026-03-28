# ADR-002: gRPC Over REST for Internal Communication

**Status:** Accepted
**Date:** 2026-03-03
**Decision makers:** Sait Dundar

## Context

Microservices need to communicate efficiently. Options considered: REST/JSON, gRPC/Protobuf, message queues (NATS/Kafka).

## Decision

Use gRPC with Protocol Buffers for all inter-service communication. Expose HTTP/REST only through the Gateway service for browser clients.

## Consequences

**Positive:**
- Binary serialization (smaller payloads, faster parsing)
- Strongly-typed contracts via `.proto` files with code generation
- Native support for client/server/bidirectional streaming (critical for chunk transfer)
- Built-in deadline propagation and cancellation

**Negative:**
- Browsers cannot call gRPC directly (hence the Gateway)
- Debugging is harder than JSON (need grpcurl/grpcui)
- Schema evolution requires careful proto field management
