package middleware

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type AuditLogger struct {
	pool *pgxpool.Pool
}

func NewAuditLogger(pool *pgxpool.Pool) *AuditLogger {
	return &AuditLogger{pool: pool}
}

func (a *AuditLogger) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		a.record(ctx, info.FullMethod, err, time.Since(start))
		return resp, err
	}
}

func (a *AuditLogger) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		a.record(ss.Context(), info.FullMethod, err, time.Since(start))
		return err
	}
}

func (a *AuditLogger) record(ctx context.Context, method string, rpcErr error, dur time.Duration) {
	userID, _ := UserIDFromContext(ctx)
	code := status.Code(rpcErr).String()

	addr := ""
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		addr = p.Addr.String()
	}

	const q = `INSERT INTO audit_logs (user_id, method, code, peer_addr, duration_ms) VALUES ($1, $2, $3, $4, $5)`

	if _, err := a.pool.Exec(ctx, q, userID, method, code, addr, dur.Milliseconds()); err != nil {
		log.Warn().Err(err).Str("method", method).Msg("failed to write audit log")
	}
}
