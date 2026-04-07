package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/auth"
	"github.com/saitddundar/vinctum-core/internal/migrator"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/grpcutil"
	"github.com/saitddundar/vinctum-core/pkg/mailer"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	migrations "github.com/saitddundar/vinctum-core/scripts/migrations"
	identityhandler "github.com/saitddundar/vinctum-core/services/identity/handler"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	logger.Init(cfg.Service.Name, cfg.Service.Version, cfg.Service.LogLevel, cfg.Service.Environment == "development")

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.Database.DSN)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to postgres")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("postgres ping failed")
	}

	if err := migrator.Run(ctx, pool, migrations.FS); err != nil {
		log.Fatal().Err(err).Msg("migration failed")
	}

	queries := repository.New(pool)
	jwtManager := auth.NewManager(cfg.Auth.JWTSecret, cfg.Auth.JWTExpiry, cfg.Auth.RefreshExpiry)
	blacklist := auth.NewTokenBlacklist(cfg.Redis.Addr)
	var ml *mailer.Mailer
	if cfg.SMTP.Host != "" && cfg.SMTP.Username != "" {
		ml = mailer.New(mailer.Config{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password,
			From:     cfg.SMTP.From,
			BaseURL:  cfg.SMTP.BaseURL,
		})
		log.Info().Str("smtp_host", cfg.SMTP.Host).Msg("mailer configured")
	} else {
		log.Warn().Msg("SMTP not configured, verification emails will not be sent")
	}

	pairing := auth.NewPairingStore(cfg.Redis.Addr)
	handler := identityhandler.NewIdentityHandler(queries, jwtManager, blacklist, pairing, ml, cfg.Auth.BcryptCost)

	lis, err := net.Listen("tcp", cfg.GRPC.Address())
	if err != nil {
		log.Fatal().Err(err).Str("addr", cfg.GRPC.Address()).Msg("failed to listen")
	}

	rl := middleware.NewRateLimiter(100, 200)
	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			middleware.UnaryMetricsInterceptor(),
			middleware.UnaryRateLimitInterceptor(rl),
			middleware.UnaryAuthInterceptor(cfg.Auth.JWTSecret),
		),
		grpc.ChainStreamInterceptor(
			middleware.StreamMetricsInterceptor(),
			middleware.StreamRateLimitInterceptor(rl),
			middleware.StreamAuthInterceptor(cfg.Auth.JWTSecret),
		),
	}

	tlsCreds, err := grpcutil.ServerCredentials(cfg.GRPC)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load TLS credentials")
	}
	if tlsCreds != nil {
		serverOpts = append(serverOpts, tlsCreds)
		log.Info().Msg("mTLS enabled")
	}

	srv := grpc.NewServer(serverOpts...)

	go func() {
		metricsAddr := fmt.Sprintf(":%d", cfg.GRPC.Port+1000)
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		log.Info().Str("addr", metricsAddr).Msg("metrics endpoint starting")
		if err := http.ListenAndServe(metricsAddr, mux); err != nil {
			log.Warn().Err(err).Msg("metrics server error")
		}
	}()

	identityv1.RegisterIdentityServiceServer(srv, handler)
	reflection.Register(srv)

	log.Info().Str("addr", cfg.GRPC.Address()).Msg("identity service starting")

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down identity service")
	srv.GracefulStop()
}
