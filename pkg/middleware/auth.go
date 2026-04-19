package middleware

import (
	"context"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

const UserIDKey contextKey = "user_id"
const EmailKey  contextKey = "email"

var publicMethods = map[string]bool{
	"/identity.v1.IdentityService/Register":             true,
	"/identity.v1.IdentityService/Login":                true,
	"/identity.v1.IdentityService/RefreshToken":         true,
	"/identity.v1.IdentityService/VerifyEmail":          true,
	"/identity.v1.IdentityService/ResendVerification":   true,
	"/identity.v1.IdentityService/ValidateToken":        true,
	"/discovery.v1.DiscoveryService/FindPeers":          true,
	"/discovery.v1.DiscoveryService/GetNodeInfo":        true,
}

func UnaryAuthInterceptor(jwtSecret string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		ctx, err := authenticate(ctx, jwtSecret)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func StreamAuthInterceptor(jwtSecret string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if publicMethods[info.FullMethod] {
			return handler(srv, ss)
		}

		_, err := authenticate(ss.Context(), jwtSecret)
		if err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func authenticate(ctx context.Context, jwtSecret string) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("authorization")
	if len(values) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authorization header")
	}

	tokenStr := strings.TrimPrefix(values[0], "Bearer ")

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, status.Errorf(codes.Unauthenticated, "unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid claims")
	}

	ctx = context.WithValue(ctx, UserIDKey, claims["sub"])
	ctx = context.WithValue(ctx, EmailKey, claims["email"])
	return ctx, nil
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(UserIDKey).(string)
	return v, ok
}
