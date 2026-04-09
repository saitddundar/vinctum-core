package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	gatewayhandler "github.com/saitddundar/vinctum-core/services/gateway/handler"
)

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

	// Wrap with CORS middleware for browser clients.
	wrapped := corsMiddleware(mux)

	httpAddr := fmt.Sprintf(":%d", envOrDefaultInt("VINCTUM_GATEWAY_HTTP_PORT", 8080))

	server := &http.Server{
		Addr:         httpAddr,
		Handler:      wrapped,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Info().Str("addr", httpAddr).Msg("gateway service starting")

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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
