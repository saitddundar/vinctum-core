package crypto_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vcrypto "github.com/saitddundar/vinctum-core/pkg/crypto"
)

func TestGenerateX25519KeyPair(t *testing.T) {
	priv, pub, err := vcrypto.GenerateX25519KeyPair()
	require.NoError(t, err)
	assert.Len(t, priv, 32)
	assert.Len(t, pub, 32)

	priv2, pub2, err := vcrypto.GenerateX25519KeyPair()
	require.NoError(t, err)
	assert.NotEqual(t, priv, priv2)
	assert.NotEqual(t, pub, pub2)
}

func TestECDH(t *testing.T) {
	privA, pubA, err := vcrypto.GenerateX25519KeyPair()
	require.NoError(t, err)
	privB, pubB, err := vcrypto.GenerateX25519KeyPair()
	require.NoError(t, err)

	secretAB, err := vcrypto.ECDH(privA, pubB)
	require.NoError(t, err)
	secretBA, err := vcrypto.ECDH(privB, pubA)
	require.NoError(t, err)

	assert.Equal(t, secretAB, secretBA)
	assert.Len(t, secretAB, 32)
}

func TestECDH_InvalidKeyLength(t *testing.T) {
	_, err := vcrypto.ECDH([]byte("short"), []byte("also-short"))
	assert.Error(t, err)
}

func TestDeriveTransferKey(t *testing.T) {
	secret := make([]byte, 32)
	ephPub := make([]byte, 32)
	recvPub := make([]byte, 32)

	key1, err := vcrypto.DeriveTransferKey(secret, "transfer-1", ephPub, recvPub)
	require.NoError(t, err)
	assert.Len(t, key1, 32)

	key2, err := vcrypto.DeriveTransferKey(secret, "transfer-1", ephPub, recvPub)
	require.NoError(t, err)
	assert.Equal(t, key1, key2, "same inputs must produce same key")

	key3, err := vcrypto.DeriveTransferKey(secret, "transfer-2", ephPub, recvPub)
	require.NoError(t, err)
	assert.NotEqual(t, key1, key3, "different transfer ID must produce different key")
}

func TestEncryptDecryptAESGCM(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("hello vinctum")

	ct, err := vcrypto.EncryptAESGCM(key, plaintext)
	require.NoError(t, err)
	assert.Greater(t, len(ct), len(plaintext))

	pt, err := vcrypto.DecryptAESGCM(key, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, pt)
}

func TestDecryptAESGCM_WrongKey(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("secret data")

	ct, err := vcrypto.EncryptAESGCM(key, plaintext)
	require.NoError(t, err)

	wrongKey := make([]byte, 32)
	wrongKey[0] = 0xff
	_, err = vcrypto.DecryptAESGCM(wrongKey, ct)
	assert.Error(t, err)
}

func TestDecryptAESGCM_ShortCiphertext(t *testing.T) {
	key := make([]byte, 32)
	_, err := vcrypto.DecryptAESGCM(key, []byte("short"))
	assert.Error(t, err)
}

func TestFullKeyExchangeFlow(t *testing.T) {
	// Receiver generates static keypair (simulates UploadDeviceKey)
	receiverPriv, receiverPub, err := vcrypto.GenerateX25519KeyPair()
	require.NoError(t, err)

	// Sender generates ephemeral keypair
	ephPriv, ephPub, err := vcrypto.GenerateX25519KeyPair()
	require.NoError(t, err)

	transferID := "test-transfer-42"

	// Sender derives key
	senderSecret, err := vcrypto.ECDH(ephPriv, receiverPub)
	require.NoError(t, err)
	senderKey, err := vcrypto.DeriveTransferKey(senderSecret, transferID, ephPub, receiverPub)
	require.NoError(t, err)

	// Receiver derives key
	receiverSecret, err := vcrypto.ECDH(receiverPriv, ephPub)
	require.NoError(t, err)
	receiverKey, err := vcrypto.DeriveTransferKey(receiverSecret, transferID, ephPub, receiverPub)
	require.NoError(t, err)

	assert.Equal(t, senderKey, receiverKey, "both sides must derive the same key")

	// Encrypt with sender key, decrypt with receiver key
	plaintext := []byte("confidential file contents")
	ct, err := vcrypto.EncryptAESGCM(senderKey, plaintext)
	require.NoError(t, err)

	pt, err := vcrypto.DecryptAESGCM(receiverKey, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, pt)
}
