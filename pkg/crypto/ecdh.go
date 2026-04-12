package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// GenerateX25519KeyPair generates a new X25519 keypair.
// Returns (privateKey [32]byte, publicKey [32]byte, error).
func GenerateX25519KeyPair() ([]byte, []byte, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating X25519 keypair: %w", err)
	}
	return priv.Bytes(), priv.PublicKey().Bytes(), nil
}

// ECDH performs X25519 Diffie-Hellman.
// localPrivate is 32 bytes, remotePubKey is 32 bytes.
// Returns 32-byte shared secret.
func ECDH(localPrivate, remotePubKey []byte) ([]byte, error) {
	curve := ecdh.X25519()

	priv, err := curve.NewPrivateKey(localPrivate)
	if err != nil {
		return nil, fmt.Errorf("parsing local private key: %w", err)
	}

	pub, err := curve.NewPublicKey(remotePubKey)
	if err != nil {
		return nil, fmt.Errorf("parsing remote public key: %w", err)
	}

	secret, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("performing ECDH: %w", err)
	}

	return secret, nil
}

// DeriveTransferKey takes a shared secret, transfer ID, ephemeral pubkey, and
// receiver static pubkey, and derives a 32-byte AES-256-GCM key via HKDF-SHA256.
// salt = ephemeralPub || receiverStaticPub (deterministic, available to both sides)
// info = []byte("vinctum-transfer-v1:" + transferID)
func DeriveTransferKey(sharedSecret []byte, transferID string, ephemeralPub, receiverStaticPub []byte) ([]byte, error) {
	salt := make([]byte, 0, len(ephemeralPub)+len(receiverStaticPub))
	salt = append(salt, ephemeralPub...)
	salt = append(salt, receiverStaticPub...)
	info := []byte("vinctum-transfer-v1:" + transferID)

	r := hkdf.New(sha256.New, sharedSecret, salt, info)

	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("deriving transfer key: %w", err)
	}

	return key, nil
}

// EncryptAESGCM encrypts plaintext with a 32-byte key using AES-256-GCM.
// Returns nonce||ciphertext (12-byte nonce prepended). Nonce is random.
func EncryptAESGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ciphertext...), nil
}

// DecryptAESGCM decrypts nonce||ciphertext with a 32-byte key.
func DecryptAESGCM(key, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 12 {
		return nil, fmt.Errorf("ciphertext too short")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := ciphertext[:12]
	data := ciphertext[12:]

	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}

	return plaintext, nil
}
