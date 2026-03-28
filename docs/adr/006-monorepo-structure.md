# ADR-006: Monorepo Over Polyrepo

**Status:** Accepted
**Date:** 2026-03-03
**Decision makers:** Sait Dundar

## Context

With 5 microservices sharing proto definitions, migrations, and common packages (config, logger, crypto, middleware), the repository structure matters. Options: one repo per service (polyrepo) vs single repository (monorepo).

## Decision

Use a monorepo. All services, proto definitions, migrations, and shared packages live under `github.com/saitddundar/vinctum-core`.

## Consequences

**Positive:**
- Atomic changes across services and shared code
- Single CI pipeline, single go.mod
- Shared proto/sqlc generation in one `make generate`
- Easier onboarding (clone once, run everything)

**Negative:**
- Larger repo size over time
- All services share the same dependency tree (go.sum)
- CI runs all tests even for single-service changes (can be optimized with path filters)
- Requires discipline to maintain clean package boundaries
