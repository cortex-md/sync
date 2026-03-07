package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type contextKey string

const (
	contextKeyAuthClaims contextKey = "auth_claims"
	contextKeyDeviceID   contextKey = "device_id"
)

func AuthMiddleware(tokens port.TokenGenerator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				WriteError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				WriteError(w, http.StatusUnauthorized, "invalid authorization header format")
				return
			}

			claims, err := tokens.ValidateAccessToken(parts[1])
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), contextKeyAuthClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func DeviceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deviceIDStr := r.Header.Get("X-Device-ID")
		if deviceIDStr == "" {
			WriteError(w, http.StatusBadRequest, "missing X-Device-ID header")
			return
		}

		deviceID, err := uuid.Parse(deviceIDStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid X-Device-ID header")
			return
		}

		ctx := context.WithValue(r.Context(), contextKeyDeviceID, deviceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetAuthClaims(ctx context.Context) *port.AccessTokenClaims {
	claims, _ := ctx.Value(contextKeyAuthClaims).(*port.AccessTokenClaims)
	return claims
}

func GetDeviceID(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(contextKeyDeviceID).(uuid.UUID)
	return id
}
