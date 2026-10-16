package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

type clientIPKeyType struct{}

var clientIPKey = clientIPKeyType{}

type IPResolver struct {
	mode     string
	hops     int
	warnOnce *sync.Once
}

func NewIPResolver(mode string, hops int) IPResolver {
	return IPResolver{mode: mode, hops: hops, warnOnce: &sync.Once{}}
}

func (res IPResolver) Resolve(r *http.Request) string {
	switch res.mode {
	case "cloudflare":
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(ip) != nil {
			return ip
		}
	case "hops":
		if ip := forwardedHop(r.Header.Values("X-Forwarded-For"), res.hops); ip != "" {
			return ip
		}
	default:
		if res.warnOnce != nil && r.Header.Get("X-Forwarded-For") != "" {
			res.warnOnce.Do(func() {
				log.Warn().Msg("requests carry X-Forwarded-For but CORTEX_PROXY=none; rate limits key on the proxy address, set CORTEX_PROXY to match your reverse proxy")
			})
		}
	}
	return remoteHost(r.RemoteAddr)
}

func forwardedHop(values []string, hops int) string {
	var chain []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				chain = append(chain, trimmed)
			}
		}
	}
	index := len(chain) - hops
	if hops < 1 || index < 0 {
		return ""
	}
	if ip := chain[index]; net.ParseIP(ip) != nil {
		return ip
	}
	return ""
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func ClientIP(resolver IPResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey, resolver.Resolve(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ClientIPFrom(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok && ip != "" {
		return ip
	}
	return remoteHost(r.RemoteAddr)
}
