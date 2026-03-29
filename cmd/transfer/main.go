package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/migrator"
	"github.com/saitddundar/vinctum-core/internal/relay"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	relayv1 "github.com/saitddundar/vinctum-core/proto/relay/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	migrations "github.com/saitddundar/vinctum-core/scripts/migrations"
	relayhandler "github.com/saitddundar/vinctum-core/services/relay/handler"
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

	// Connect to discovery service for peer resolution.
	var discoveryClient discoveryv1.DiscoveryServiceClient
	discoveryAddr := os.Getenv("VINCTUM_DISCOVERY_ADDR")
	if discoveryAddr == "" {
		discoveryAddr = "localhost:50052"
	}

	discoveryConn, err := grpc.NewClient(discoveryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Warn().Err(err).Str("addr", discoveryAddr).Msg("could not dial discovery service")
	} else {
		discoveryClient = discoveryv1.NewDiscoveryServiceClient(discoveryConn)
		defer discoveryConn.Close()
		log.Info().Str("addr", discoveryAddr).Msg("connected to discovery service")
	}

	// Build relay client for inter-node chunk forwarding.
	var relayClient *relay.Client
	if discoveryClient != nil {
		peerPool := relay.NewPeerPool(discoveryClient, 3, 30*time.Second)
		relayClient = relay.NewClient(peerPool)
		defer peerPool.Close()
	}

	nodeID := os.Getenv("VINCTUM_NODE_ID")
	if nodeID == "" {
		nodeID = cfg.Service.Name
	}

	handler := transferhandler.NewTransferHandler(queries, routingClient, chunkStore, relayClient, nodeID)

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

	// Register RelayService so this node can receive relayed chunks.
	relayHandler := relayhandler.NewRelayHandler(nodeID, chunkStore, relayClient)
	relayv1.RegisterRelayServiceServer(srv, relayHandler)

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
