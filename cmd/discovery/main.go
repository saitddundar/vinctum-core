package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	discoveryhandler "github.com/saitddundar/vinctum-core/services/discovery/handler"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	logger.Init(cfg.Service.Name, cfg.Service.Version, cfg.Service.LogLevel, cfg.Service.Environment == "development")

	lis, err := net.Listen("tcp", cfg.GRPC.Address())
	if err != nil {
		log.Fatal().Err(err).Str("addr", cfg.GRPC.Address()).Msg("failed to listen")
	}

	srv := grpc.NewServer()

	discoveryv1.RegisterDiscoveryServiceServer(srv, discoveryhandler.NewDiscoveryHandler())
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
}
