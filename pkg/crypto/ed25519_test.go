package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestVerifyAnnouncement(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	nodeID := "QmTestNode123"
	addrs := []string{"/ip4/1.2.3.4/tcp/4001", "/ip6/::1/udp/4001/quic-v1"}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	payload := AnnouncePayload(nodeID, addrs, pubB64)
	sig := ed25519.Sign(priv, payload)

	if err := VerifyAnnouncement(nodeID, addrs, pubB64, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	// Tampered node_id should fail.
	if err := VerifyAnnouncement("tampered", addrs, pubB64, sig); err == nil {
		t.Fatal("tampered node_id accepted")
	}

	// Tampered address should fail.
	if err := VerifyAnnouncement(nodeID, []string{"/ip4/6.6.6.6/tcp/9999"}, pubB64, sig); err == nil {
		t.Fatal("tampered addrs accepted")
	}

	// Wrong key should fail.
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2B64 := base64.StdEncoding.EncodeToString(pub2)
	if err := VerifyAnnouncement(nodeID, addrs, pub2B64, sig); err == nil {
		t.Fatal("wrong key accepted")
	}
}
