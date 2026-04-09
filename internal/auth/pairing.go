package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	pairingPrefix = "pairing:"
	pairingTTL    = 5 * time.Minute
	codeChars     = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I
	codeLength    = 6
)

type PairingData struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

type PairingStore struct {
	rdb *redis.Client
}

func NewPairingStore(addr string) *PairingStore {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	return &PairingStore{rdb: rdb}
}

func (p *PairingStore) GenerateCode(ctx context.Context, userID, deviceID string) (string, error) {
	code := randomCode()
	data, err := json.Marshal(PairingData{UserID: userID, DeviceID: deviceID})
	if err != nil {
		return "", fmt.Errorf("marshal pairing data: %w", err)
	}

	key := pairingPrefix + code
	if err := p.rdb.Set(ctx, key, data, pairingTTL).Err(); err != nil {
		return "", fmt.Errorf("store pairing code: %w", err)
	}
	return code, nil
}

func (p *PairingStore) RedeemCode(ctx context.Context, code string) (*PairingData, error) {
	key := pairingPrefix + code

	val, err := p.rdb.GetDel(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("invalid or expired pairing code")
		}
		return nil, fmt.Errorf("redeem pairing code: %w", err)
	}

	var data PairingData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return nil, fmt.Errorf("unmarshal pairing data: %w", err)
	}
	return &data, nil
}

func randomCode() string {
	b := make([]byte, codeLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeChars))))
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b[i] = codeChars[n.Int64()]
	}
	return string(b)
}
