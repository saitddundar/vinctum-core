# ADR-007: AES-256-GCM for End-to-End Encryption

**Status:** Accepted
**Date:** 2026-03-29

## Context

Vinctum transfers data between peers through a relay server. Data at rest on the relay (chunk files) must be unreadable to the server operator. Clients need a simple, well-understood symmetric encryption scheme that provides both confidentiality and integrity.

## Decision

Use AES-256-GCM for encrypting chunk data end-to-end.

- **Key size:** 256-bit (32 bytes), base64-encoded for transport/storage.
- **Nonce:** 12-byte random nonce prepended to each ciphertext.
- **Scope:** Per-transfer key provided by the sender at initiation time.
- **Integrity:** GCM's authentication tag protects against tampering. Additionally, a SHA-256 content hash of the full plaintext is verified on transfer completion.
- **Chunk hash:** Verified against plaintext data before encryption on send, and computed from decrypted data on receive.

## Alternatives Considered

| Option | Rejected Because |
|--------|-----------------|
| AES-CBC + HMAC | Two-step construct, more error-prone than AEAD |
| ChaCha20-Poly1305 | Excellent choice but Go stdlib AES-GCM is hardware-accelerated on most platforms |
| RSA/asymmetric per-chunk | Too slow for large file transfers, unnecessary when peers can share a symmetric key |
| NaCl secretbox | Would work, but AES-GCM is the Go ecosystem standard and aligns with TLS 1.3 cipher suites |

## Consequences

- Server never sees plaintext; even a compromised relay leaks nothing.
- Key management is the client's responsibility; the server stores the key only for the transfer session.
- Chunk hash verification is skipped for the raw stored data when encryption is active (verified against plaintext instead).
- Future: key exchange via Diffie-Hellman or libp2p noise protocol could replace manual key sharing.
