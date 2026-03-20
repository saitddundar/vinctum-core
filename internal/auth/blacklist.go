package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const blacklistPrefix = "token:blacklist:"

type TokenBlacklist struct {
	rdb *redis.Client
}

func NewTokenBlacklist(addr string) *TokenBlacklist {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	return &TokenBlacklist{rdb: rdb}
}

func (b *TokenBlacklist) Add(ctx context.Context, token string, expiry time.Duration) error {
	key := blacklistPrefix + token
	if err := b.rdb.Set(ctx, key, 1, expiry).Err(); err != nil {
		return fmt.Errorf("blacklisting token: %w", err)
	}
	return nil
}

func (b *TokenBlacklist) IsBlacklisted(ctx context.Context, token string) (bool, error) {
	key := blacklistPrefix + token
	n, err := b.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("checking blacklist: %w", err)
	}
	return n > 0, nil
}

func (b *TokenBlacklist) Ping(ctx context.Context) error {
	return b.rdb.Ping(ctx).Err()
}
