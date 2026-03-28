package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/migrator"
	"github.com/saitddundar/vinctum-core/internal/p2p"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	migrations "github.com/saitddundar/vinctum-core/scripts/migrations"
	discoveryhandler "github.com/saitddundar/vinctum-core/services/discovery/handler"
	"github.com/saitddundar/vinctum-core/services/discovery/repository"
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
	handler := discoveryhandler.NewDiscoveryHandler(queries)

	p2pNode, err := p2p.NewNode(ctx, p2p.NodeConfig{
		ListenAddrs: cfg.P2P.ListenAddresses,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start p2p node")
	}

	lis, err := net.Listen("tcp", cfg.GRPC.Address())
	if err != nil {
		log.Fatal().Err(err).Str("addr", cfg.GRPC.Address()).Msg("failed to listen")
	}

	rl := middleware.NewRateLimiter(100, 200)
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			middleware.UnaryRateLimitInterceptor(rl),
			middleware.UnaryAuthInterceptor(cfg.Auth.JWTSecret),
		),
		grpc.ChainStreamInterceptor(
			middleware.StreamRateLimitInterceptor(rl),
			middleware.StreamAuthInterceptor(cfg.Auth.JWTSecret),
		),
	)
	discoveryv1.RegisterDiscoveryServiceServer(srv, handler)
	reflection.Register(srv)

	log.Info().Str("addr", cfg.GRPC.Address()).Msg("discovery service starting")

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down discovery service")
	srv.GracefulStop()
	_ = p2pNode.Close()
}
