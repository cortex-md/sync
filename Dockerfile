FROM golang:1.26.9-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X github.com/cortexnotes/cortex-sync/internal/server.Version=${VERSION}" -o /cortex-sync ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/cortex-md/sync" \
	org.opencontainers.image.description="Cortex sync server" \
	org.opencontainers.image.licenses="MIT"

COPY --from=builder /cortex-sync /usr/local/bin/cortex-sync
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/cortex-sync"]
