FROM golang:1.25.11-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /cortex-sync ./cmd/server

FROM alpine:3.21

LABEL org.opencontainers.image.source="https://github.com/cortex-md/sync" \
	org.opencontainers.image.description="Cortex sync server" \
	org.opencontainers.image.licenses="MIT"

RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S cortex && adduser -S -G cortex -H -D cortex

COPY --from=builder /cortex-sync /usr/local/bin/cortex-sync
COPY --from=builder /app/migrations /migrations

ENV CORTEX_DATABASE_MIGRATIONS_PATH=file:///migrations

EXPOSE 8080

USER cortex:cortex

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["cortex-sync"]
