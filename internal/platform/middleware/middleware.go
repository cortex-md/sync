package middleware

import (
	"bufio"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !validRequestID(requestID) {
			requestID = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := r.Context()
		logger := log.With().Str("request_id", requestID).Logger()
		ctx = logger.WithContext(ctx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(ww, r)
		duration := time.Since(start)

		logEvent := zerolog.Ctx(r.Context()).Info()
		var msg = "request completed"

		if ww.statusCode >= 400 {
			msg = "request failed"
			if ww.statusCode >= 500 {
				logEvent = zerolog.Ctx(r.Context()).Error()
			} else {
				logEvent = zerolog.Ctx(r.Context()).Warn()
			}
			logEvent = logEvent.
				Str("query", sanitizeRawQuery(r.URL.RawQuery)).
				Str("client_ip", ClientIPFrom(r)).
				Str("user_agent", r.UserAgent())
		}

		logEvent.
			Str("method", r.Method).
			Str("route", routePattern(r)).
			Int("status", ww.statusCode).
			Dur("duration", duration).
			Int("bytes", ww.bytesWritten).
			Msg(msg)
	})
}

func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				zerolog.Ctx(r.Context()).Error().
					Interface("panic", err).
					Str("route", routePattern(r)).
					Msg("panic recovered")
				WriteLimited(w, http.StatusInternalServerError, "internal_error", "internal server error", 0)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	rw.statusCode = http.StatusSwitchingProtocols
	return hijacker.Hijack()
}

func sanitizeRawQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "invalid_query"
	}
	for key := range values {
		normalized := strings.ToLower(key)
		if strings.Contains(normalized, "token") ||
			strings.Contains(normalized, "ticket") ||
			strings.Contains(normalized, "secret") ||
			strings.Contains(normalized, "authorization") {
			values[key] = []string{"[redacted]"}
		}
	}
	return values.Encode()
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/account/") {
			header.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
