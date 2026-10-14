package config

import (
	"cmp"
	"fmt"
	"net/url"
	"os"
	"strings"
)

func resolveStorageConfig() (S3Config, error) {
	backend, err := normalizeStorageBackend(os.Getenv("CORTEX_STORAGE_BACKEND"))
	if err != nil {
		return S3Config{}, err
	}

	endpoint := storageValue("CORTEX_STORAGE_ENDPOINT")
	accessKey := storageValue("CORTEX_STORAGE_ACCESS_KEY")
	secretKey := storageValue("CORTEX_STORAGE_SECRET_KEY")
	bucket := storageValue("CORTEX_STORAGE_BUCKET")
	region := storageValue("CORTEX_STORAGE_REGION")

	if backend == "local" {
		endpoint = cmp.Or(endpoint, "localhost:9000")
		accessKey = cmp.Or(accessKey, "minioadmin")
		secretKey = cmp.Or(secretKey, "minioadmin")
	}
	bucket = cmp.Or(bucket, "cortex-snapshots")
	if backend == "r2" {
		region = cmp.Or(region, "auto")
	}
	region = cmp.Or(region, "us-east-1")

	normalizedEndpoint, endpointUseSSL, err := normalizeStorageEndpoint(endpoint)
	if err != nil {
		return S3Config{}, err
	}
	useSSL := backend != "local"
	if endpointUseSSL != nil {
		useSSL = *endpointUseSSL
	}

	return S3Config{
		Backend:      backend,
		Endpoint:     normalizedEndpoint,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Bucket:       bucket,
		UseSSL:       useSSL,
		Region:       region,
		CreateBucket: backend == "local",
	}, nil
}

func normalizeStorageBackend(backend string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(backend))
	switch normalized {
	case "":
		return "local", nil
	case "local", "s3", "r2":
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported storage backend %q", backend)
	}
}

func storageValue(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func normalizeStorageEndpoint(endpoint string) (string, *bool, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return "", nil, nil
	}

	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", nil, fmt.Errorf("invalid storage endpoint %q: %w", endpoint, err)
		}
		if parsed.Host == "" {
			return "", nil, fmt.Errorf("invalid storage endpoint %q: missing host", endpoint)
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return "", nil, fmt.Errorf("invalid storage endpoint %q: endpoint must not include a path", endpoint)
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", nil, fmt.Errorf("invalid storage endpoint %q: endpoint must not include query or fragment", endpoint)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", nil, fmt.Errorf("invalid storage endpoint %q: unsupported scheme", endpoint)
		}
		useSSL := parsed.Scheme == "https"
		return parsed.Host, &useSSL, nil
	}

	if strings.Contains(trimmed, "/") {
		return "", nil, fmt.Errorf("invalid storage endpoint %q: endpoint must not include a path", endpoint)
	}

	return trimmed, nil, nil
}
