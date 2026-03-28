# ADR-004: sqlc Over ORM for Database Access

**Status:** Accepted
**Date:** 2026-03-03
**Decision makers:** Sait Dundar

## Context

Services need to interact with PostgreSQL. Options considered: raw database/sql, GORM, sqlx, sqlc, ent.

## Decision

Use sqlc to generate type-safe Go code from SQL queries. Each service defines its own `queries.sql` file and gets a generated `Querier` interface.

## Consequences

**Positive:**
- No runtime reflection or magic -- just plain SQL
- Compile-time type safety (sqlc validates queries against schema)
- Generated `Querier` interface enables unit testing with fakes
- No ORM overhead or N+1 query surprises
- SQL is the source of truth, not Go structs

**Negative:**
- Must write raw SQL (less productive for simple CRUD)
- Schema changes require re-running `sqlc generate`
- Less automatic migration tooling compared to GORM AutoMigrate
- Separate sqlc config per service adds maintenance overhead
