package middleware

import (
	"io"
	"net/http"
)

type limitedBody struct {
	w      http.ResponseWriter
	source io.ReadCloser
	reader io.ReadCloser
	limit  int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.reader == nil {
		b.reader = b.source
		if b.limit > 0 {
			b.reader = http.MaxBytesReader(b.w, b.source, b.limit)
		}
	}
	return b.reader.Read(p)
}

func (b *limitedBody) Close() error {
	return b.source.Close()
}

func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				r.Body = &limitedBody{w: w, source: r.Body, limit: maxBytes}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func AllowBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body, ok := r.Body.(*limitedBody); ok && body.reader == nil {
				body.limit = maxBytes
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireContentLength(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength < 0 {
				WriteLimited(w, http.StatusLengthRequired, "length_required", "Content-Length is required", 0)
				return
			}
			if maxBytes > 0 && r.ContentLength > maxBytes {
				WriteLimited(w, http.StatusRequestEntityTooLarge, "file_too_large", "request body is too large", 0)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
