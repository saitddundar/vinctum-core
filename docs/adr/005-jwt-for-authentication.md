# ADR-005: JWT with HMAC-SHA256 for Authentication

**Status:** Accepted
**Date:** 2026-03-10
**Decision makers:** Sait Dundar

## Context

Services need to authenticate requests. Options considered: session cookies, OAuth2 with opaque tokens, JWT (symmetric HMAC vs asymmetric RSA/Ed25519).

## Decision

Use JWT with HMAC-SHA256 signing. Access tokens are short-lived (24h), refresh tokens are long-lived (168h). A Redis-backed blacklist invalidates tokens on logout. Refresh token rotation is enforced (old refresh is blacklisted on each refresh).

## Consequences

**Positive:**
- Stateless verification at each service (no DB lookup per request)
- Single shared secret simplifies key management in a private cluster
- Refresh token rotation limits the blast radius of stolen tokens
- Redis blacklist enables immediate token revocation

**Negative:**
- Shared secret means any compromised service can forge tokens (mitigated by mTLS between services in production)
- HMAC requires all verifiers to hold the signing key (asymmetric keys would allow read-only verification)
- Token blacklist adds a Redis dependency to the hot path
