package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	gatewayhandler "github.com/saitddundar/vinctum-core/services/gateway/handler"
)

const maxRequestBodySize = 10 * 1024 * 1024 // 10 MB

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	logger.Init(cfg.Service.Name, cfg.Service.Version, cfg.Service.LogLevel, cfg.Service.Environment == "development")

	// Resolve backend service addresses from config or env.
	addrs := gatewayhandler.ServiceAddresses{
		Identity:  envOrDefault("VINCTUM_GATEWAY_IDENTITY_ADDR", "localhost:50051"),
		Discovery: envOrDefault("VINCTUM_GATEWAY_DISCOVERY_ADDR", "localhost:50052"),
		Routing:   envOrDefault("VINCTUM_GATEWAY_ROUTING_ADDR", "localhost:50053"),
		Transfer:  envOrDefault("VINCTUM_GATEWAY_TRANSFER_ADDR", "localhost:50054"),
		ML:        envOrDefault("VINCTUM_GATEWAY_ML_ADDR", ""),
	}

	gw, err := gatewayhandler.NewGatewayHandler(addrs, cfg.Service.Version)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create gateway handler")
	}
	defer gw.Close()

	if cfg.ML.APIKey != "" {
		gw.SetMLAPIKey(cfg.ML.APIKey)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	gw.RegisterRoutes(mux)

	// Allowed CORS origins from env (comma-separated) or default for dev.
	allowedOrigins := parseOrigins(envOrDefault("VINCTUM_CORS_ORIGINS", "http://localhost:5173,http://localhost:3000,http://localhost:8081,http://localhost:8082"))

	// HTTP rate limiter: 20 req/s general, 5 req/s for auth endpoints.
	rl := newHTTPRateLimiter(
		envOrDefaultFloat("VINCTUM_HTTP_RATE_RPS", 20),
		envOrDefaultInt("VINCTUM_HTTP_RATE_BURST", 60),
	)
	rlAuth := newHTTPRateLimiter(
		envOrDefaultFloat("VINCTUM_HTTP_AUTH_RATE_RPS", 5),
		envOrDefaultInt("VINCTUM_HTTP_AUTH_RATE_BURST", 10),
	)

	wrapped := rateLimitMiddleware(rl, rlAuth, securityMiddleware(allowedOrigins, mux))

	httpAddr := fmt.Sprintf(":%d", envOrDefaultInt("VINCTUM_GATEWAY_HTTP_PORT", 8080))

	server := &http.Server{
		Addr:              httpAddr,
		Handler:           wrapped,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Info().Str("addr", httpAddr).Int("cors_origins", len(allowedOrigins)).Msg("gateway service starting")

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down gateway service")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("gateway shutdown error")
	}
}

func securityMiddleware(allowedOrigins map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")

		// CORS with origin whitelist
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Add("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Add("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Add("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Content-Type validation for requests with body
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			ct := r.Header.Get("Content-Type")
			// Allow multipart for file uploads, require JSON for everything else
			if !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
				http.Error(w, `{"error":"Content-Type must be application/json or multipart/form-data"}`, http.StatusUnsupportedMediaType)
				return
			}
		}

		// Request body size limit
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		}

		next.ServeHTTP(w, r)
	})
}

func parseOrigins(raw string) map[string]bool {
	origins := make(map[string]bool)
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			origins[o] = true
		}
	}
	return origins
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return fallback
}

func envOrDefaultFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		var f float64
		fmt.Sscanf(v, "%f", &f)
		if f > 0 {
			return f
		}
	}
	return fallback
}

// ─── HTTP Rate Limiter ──────────────────────────────────────────────────────

type httpLimiterEntry struct {
	tokens   float64
	lastSeen time.Time
}

type httpRateLimiter struct {
	mu      sync.Mutex
	clients map[string]*httpLimiterEntry
	rate    float64 // tokens per second
	burst   float64
}

func newHTTPRateLimiter(rps float64, burst int) *httpRateLimiter {
	rl := &httpRateLimiter{
		clients: make(map[string]*httpLimiterEntry),
		rate:    rps,
		burst:   float64(burst),
	}
	go rl.cleanup()
	return rl
}

func (rl *httpRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	e, ok := rl.clients[ip]
	if !ok {
		rl.clients[ip] = &httpLimiterEntry{tokens: rl.burst - 1, lastSeen: now}
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

func (rl *httpRateLimiter) cleanup() {
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

// clientIP extracts the real client IP, respecting X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// authPaths are endpoints that get the stricter per-IP rate limit.
var authPaths = map[string]bool{
	"/api/v1/auth/login":    true,
	"/api/v1/auth/register": true,
}

func rateLimitMiddleware(general, auth *httpRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		rl := general
		if authPaths[r.URL.Path] {
			rl = auth
		}
		if !rl.allow(ip) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
