package middleware

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type limiterEntry struct {
	tokens    float64
	lastSeen  time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*limiterEntry
	rate     float64 // tokens per second
	burst    float64 // max tokens
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*limiterEntry),
		rate:    rps,
		burst:   float64(burst),
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	e, ok := rl.clients[key]
	if !ok {
		rl.clients[key] = &limiterEntry{tokens: rl.burst - 1, lastSeen: now}
		return true
	}

	elapsed := now.Sub(e.lastSeen).Seconds()
	e.tokens += elapsed * rl.rate
	if e.tokens > rl.burst {
		e.tokens = rl.burst
	}
	e.lastSeen = now

	if e.tokens < 1 {
		return false
	}
	e.tokens--
	return true
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, e := range rl.clients {
			if e.lastSeen.Before(cutoff) {
				delete(rl.clients, k)
			}
		}
		rl.mu.Unlock()
	}
}

func peerAddr(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}

func UnaryRateLimitInterceptor(rl *RateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !rl.allow(peerAddr(ctx)) {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

func StreamRateLimitInterceptor(rl *RateLimiter) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !rl.allow(peerAddr(ss.Context())) {
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}
