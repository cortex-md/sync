package middleware

import (
	"net/http"
	"strings"
)

func BodyLimit(maxJSONBytes int64, maxUploadBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := bodyLimitForRequest(r, maxJSONBytes, maxUploadBytes)
			if limit > 0 && r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bodyLimitForRequest(r *http.Request, maxJSONBytes int64, maxUploadBytes int64) int64 {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return 0
	}

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/octet-stream") {
		return maxUploadBytes
	}

	return maxJSONBytes
}
