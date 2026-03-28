# ADR-003: libp2p for Peer-to-Peer Networking

**Status:** Accepted
**Date:** 2026-03-03
**Decision makers:** Sait Dundar

## Context

Vinctum's core value proposition is decentralized data transfer. Nodes must discover each other, traverse NATs, and optionally relay traffic through intermediaries. Options considered: raw TCP/QUIC, WireGuard mesh, libp2p, custom protocol.

## Decision

Use go-libp2p as the P2P networking layer with Kademlia DHT for peer discovery and circuit relay for NAT traversal.

## Consequences

**Positive:**
- Battle-tested in production (IPFS, Filecoin, Ethereum 2.0)
- Built-in Kademlia DHT, mDNS, relay, hole punching, NAT port mapping
- Transport-agnostic (TCP, QUIC, WebSocket, WebTransport)
- Cryptographic peer identity (Ed25519 key pairs)
- Active Go implementation with good community support

**Negative:**
- Large dependency tree
- Complex configuration surface
- Debugging P2P issues is harder than client-server
- DHT bootstrap requires initial known peers
