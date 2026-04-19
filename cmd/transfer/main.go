package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/migrator"
	"github.com/saitddundar/vinctum-core/internal/p2p"
	"github.com/saitddundar/vinctum-core/internal/relay"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/grpcutil"
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

	clientCreds, err := grpcutil.ClientCredentials(cfg.GRPC)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load client TLS credentials")
	}

	routingConn, err := grpc.NewClient(routingAddr, clientCreds)
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

	discoveryConn, err := grpc.NewClient(discoveryAddr, clientCreds)
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

	// Initialise P2P node for direct peer-to-peer transfers.
	if cfg.P2P.EnableRelay || cfg.P2P.EnableDHT {
		p2pNode, p2pErr := p2p.NewNode(ctx, p2p.NodeConfig{
			ListenAddrs:     cfg.P2P.ListenAddresses,
			BootstrapPeers:  cfg.P2P.BootstrapPeers,
			EnableRelay:     cfg.P2P.EnableRelay,
			EnableDHT:       cfg.P2P.EnableDHT,
			EnableHolePunch: cfg.P2P.EnableHolePunch,
		})
		if p2pErr != nil {
			log.Warn().Err(p2pErr).Msg("failed to start p2p node, P2P transfers disabled")
		} else {
			defer p2pNode.Close()

			// Create transfer protocol and register stream handler.
			tp := p2p.NewTransferProtocol(p2pNode.Host, chunkStore, func(transferID string, chunkIndex, totalChunks int32) {
				// Update DB progress when chunks arrive via P2P.
				_ = queries.UpdateTransferProgress(ctx, repository.UpdateTransferProgressParams{
					TransferID: transferID,
					ChunksDone: chunkIndex + 1,
				})
				if chunkIndex+1 >= totalChunks {
					_ = queries.CompleteTransfer(ctx, transferID)
				}
			})
			tp.RegisterHandler()

			handler.SetP2P(&p2pAdapter{tp: tp})
			log.Info().Str("peer_id", p2pNode.Host.ID().String()).Msg("p2p transfer protocol enabled")
		}
	}

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

	transferv1.RegisterTransferServiceServer(srv, handler)

	// Register RelayService so this node can receive relayed chunks.
	rerouter := relay.NewRerouter(routingClient)
	replicator := relay.NewReplicator(relayClient, discoveryClient)
	relayHandler := relayhandler.NewRelayHandler(nodeID, chunkStore, relayClient, rerouter, replicator)
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

// p2pAdapter adapts the internal p2p.TransferProtocol to the handler's
// P2PTransferer interface, converting peer.ID to/from strings.
type p2pAdapter struct {
	tp *p2p.TransferProtocol
}

func (a *p2pAdapter) SendFile(ctx context.Context, peerIDStr string, transferID string, totalChunks int32, totalSizeBytes int64, contentHash string, chunkReader func(int32) ([]byte, string, error)) error {
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		return fmt.Errorf("invalid peer ID %q: %w", peerIDStr, err)
	}
	return a.tp.SendFile(ctx, pid, transferID, totalChunks, totalSizeBytes, contentHash, chunkReader)
}

func (a *p2pAdapter) IsReachable(ctx context.Context, peerIDStr string) bool {
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		return false
	}
	return a.tp.IsReachable(ctx, pid)
}

func (a *p2pAdapter) PeerID() string {
	return a.tp.PeerID().String()
}

func (a *p2pAdapter) Addrs() []string {
	return a.tp.Addrs()
}
