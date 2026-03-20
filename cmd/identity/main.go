package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/auth"
	"github.com/saitddundar/vinctum-core/pkg/config"
	"github.com/saitddundar/vinctum-core/pkg/logger"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
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

	jwtManager := auth.NewManager(cfg.Auth.JWTSecret, cfg.Auth.JWTExpiry, cfg.Auth.RefreshExpiry)
	userRepo := repository.NewInMemoryUserRepository()
	handler := identityhandler.NewIdentityHandler(userRepo, jwtManager, cfg.Auth.BcryptCost)

	lis, err := net.Listen("tcp", cfg.GRPC.Address())
	if err != nil {
		log.Fatal().Err(err).Str("addr", cfg.GRPC.Address()).Msg("failed to listen")
	}

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(middleware.UnaryAuthInterceptor(cfg.Auth.JWTSecret)),
		grpc.StreamInterceptor(middleware.StreamAuthInterceptor(cfg.Auth.JWTSecret)),
	)

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
