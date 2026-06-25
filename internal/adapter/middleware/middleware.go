package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
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
				Str("remote_addr", r.RemoteAddr).
				Str("user_agent", r.UserAgent())
		}

		logEvent.
			Str("method", r.Method).
			Str("path", r.URL.Path).
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
					Msg("panic recovered")
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
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
