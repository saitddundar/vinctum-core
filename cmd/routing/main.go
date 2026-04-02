package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/intelligence"
	"github.com/saitddundar/vinctum-core/internal/migrator"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	migrations "github.com/saitddundar/vinctum-core/scripts/migrations"
	routinghandler "github.com/saitddundar/vinctum-core/services/routing/handler"
	"github.com/saitddundar/vinctum-core/services/routing/repository"
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
	handler := routinghandler.NewRoutingHandler(queries)

	// Wire intelligence module for smart routing decisions.
	collector := intelligence.NewCollector(10 * time.Minute)
	scorer := intelligence.NewScorer(collector, intelligence.DefaultWeights())
	detector := intelligence.NewAnomalyDetector(collector, intelligence.DefaultAnomalyConfig())
	handler.SetIntelligence(intelligence.NewRouterAdapter(scorer, detector))
	log.Info().Msg("network intelligence enabled for routing")

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

	routingv1.RegisterRoutingServiceServer(srv, handler)
	reflection.Register(srv)

	// Subscribe to discovery peer updates to auto-populate routing table.
	discoveryAddr := os.Getenv("VINCTUM_DISCOVERY_ADDR")
	if discoveryAddr == "" {
		discoveryAddr = "localhost:50052"
	}

	nodeID := os.Getenv("VINCTUM_NODE_ID")
	if nodeID == "" {
		nodeID = cfg.Service.Name
	}

	discoveryConn, err := grpc.NewClient(discoveryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Warn().Err(err).Msg("could not dial discovery service, auto-routing disabled")
	} else {
		dc := discoveryv1.NewDiscoveryServiceClient(discoveryConn)
		defer discoveryConn.Close()

		go watchPeerUpdates(ctx, dc, handler, nodeID)
		log.Info().Str("addr", discoveryAddr).Msg("subscribed to discovery peer updates")
	}

	log.Info().Str("addr", cfg.GRPC.Address()).Msg("routing service starting")

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down routing service")
	srv.GracefulStop()
}

func watchPeerUpdates(ctx context.Context, dc discoveryv1.DiscoveryServiceClient, handler *routinghandler.RoutingHandler, nodeID string) {
	stream, err := dc.StreamPeerUpdates(ctx, &discoveryv1.StreamPeerUpdatesRequest{NodeId: nodeID})
	if err != nil {
		log.Warn().Err(err).Msg("failed to open peer updates stream")
		return
	}

	for {
		update, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.Info().Msg("peer updates stream closed")
				return
			}
			log.Warn().Err(err).Msg("peer updates stream error")
			return
		}

		if update.Peer == nil {
			continue
		}

		peer := update.Peer
		switch update.Type {
		case discoveryv1.PeerUpdate_PEER_JOINED, discoveryv1.PeerUpdate_PEER_UPDATED:
			// Add a direct route entry (metric=1) for the new/updated peer.
			_, _ = handler.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{
				NodeId: nodeID,
				Entries: []*routingv1.RouteEntry{{
					TargetNodeId: peer.NodeId,
					NextHopId:    peer.NodeId,
					Metric:       1,
					LatencyMs:    0,
				}},
			})
			log.Debug().Str("peer", peer.NodeId).Msg("route added from peer update")

		case discoveryv1.PeerUpdate_PEER_LEFT:
			log.Debug().Str("peer", peer.NodeId).Msg("peer left, route may need cleanup")
		}
	}
}
