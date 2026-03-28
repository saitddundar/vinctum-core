# ADR-001: Microservices Over Monolith

**Status:** Accepted
**Date:** 2026-03-03
**Decision makers:** Sait Dundar

## Context

Vinctum needs to handle identity, peer discovery, routing, file transfer, and an HTTP gateway. These domains have different scaling profiles, failure modes, and release cadences. A monolithic approach would simplify initial development but would couple all components into a single deploy unit.

## Decision

Adopt a microservice architecture with one service per bounded context: Identity, Discovery, Routing, Transfer, and Gateway.

## Consequences

**Positive:**
- Independent scaling (Transfer needs more CPU/IO than Identity)
- Fault isolation (Discovery crash does not take down Transfer)
- Independent deployability and versioning
- Clear ownership boundaries per service

**Negative:**
- Operational complexity (5 services + backing stores)
- Network latency for inter-service calls
- Distributed transaction complexity (eventual consistency)
- Requires service discovery and health checking infrastructure
