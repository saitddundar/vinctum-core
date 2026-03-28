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
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	migrations "github.com/saitddundar/vinctum-core/scripts/migrations"
	transferhandler "github.com/saitddundar/vinctum-core/services/transfer/handler"
	"github.com/saitddundar/vinctum-core/services/transfer/repository"
	transferstorage "github.com/saitddundar/vinctum-core/services/transfer/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	var routingClient routingv1.RoutingServiceClient
	routingAddr := os.Getenv("VINCTUM_ROUTING_ADDR")
	if routingAddr == "" {
		routingAddr = "localhost:50053"
	}

	routingConn, err := grpc.NewClient(routingAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Warn().Err(err).Str("addr", routingAddr).Msg("could not dial routing service, route resolution disabled")
	} else {
		routingClient = routingv1.NewRoutingServiceClient(routingConn)
		defer routingConn.Close()
		log.Info().Str("addr", routingAddr).Msg("connected to routing service")
	}

	//Initialise chunk storage backend
	chunkDir := os.Getenv("VINCTUM_CHUNK_DIR")
	if chunkDir == "" {
		chunkDir = "./data/chunks"
	}

	chunkStore, err := transferstorage.NewFileStore(chunkDir)
	if err != nil {
		log.Fatal().Err(err).Str("dir", chunkDir).Msg("failed to init chunk storage")
	}
	log.Info().Str("dir", chunkDir).Msg("chunk storage initialised")

	handler := transferhandler.NewTransferHandler(queries, routingClient, chunkStore)

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

	transferv1.RegisterTransferServiceServer(srv, handler)
	reflection.Register(srv)

	log.Info().Str("addr", cfg.GRPC.Address()).Msg("transfer service starting")

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down transfer service")
	srv.GracefulStop()
}
