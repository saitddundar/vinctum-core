package crypto

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// AnnouncePayload builds the canonical byte payload that must be signed
// for a peer announcement: node_id || "\n" || addr1 || "\n" || ... || "\n" || public_key.
func AnnouncePayload(nodeID string, addrs []string, publicKey string) []byte {
	parts := make([]string, 0, 2+len(addrs))
	parts = append(parts, nodeID)
	parts = append(parts, addrs...)
	parts = append(parts, publicKey)
	return []byte(strings.Join(parts, "\n"))
}

// VerifyAnnouncement checks that signature is a valid Ed25519 signature
// over AnnouncePayload(nodeID, addrs, publicKeyB64) using the given base64-encoded public key.
func VerifyAnnouncement(nodeID string, addrs []string, publicKeyB64 string, signature []byte) error {
	pubBytes, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return fmt.Errorf("invalid public key encoding: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length: got %d, want %d", len(pubBytes), ed25519.PublicKeySize)
	}

	payload := AnnouncePayload(nodeID, addrs, publicKeyB64)
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), payload, signature) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
