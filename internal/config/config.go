package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server       ServerConfig
	Database     DatabaseConfig
	S3           S3Config
	Auth         AuthConfig
	RateLimit    RateLimitConfig
	Metrics      MetricsConfig
	CORS         CORSConfig
	Sync         SyncConfig
	Collab       CollabConfig
	Subscription SubscriptionConfig
	DevOps       DevOpsConfig
	UseFakeRepos bool
}

type DevOpsConfig struct {
	DiscordWebhookURL string
	DiscordUsername   string
	DiscordTimeout    time.Duration
}

type SubscriptionConfig struct {
	Enabled             bool
	StripeSecretKey     string
	StripePriceID       string
	StripeWebhookSecret string
	CacheTTL            time.Duration
	RenewalGrace        time.Duration
}

type SyncConfig struct {
	MaxDeltasBeforeSnapshot int
	MaxDeltaSizeRatio       float64
	MaxFileSize             int64
	MaxSnapshotsPerFile     int
	EventRetention          time.Duration
}

type CollabConfig struct {
	MaxPeersPerRoom int
	FlushInterval   time.Duration
	MaxBufBytes     int
}

type RateLimitConfig struct {
	RequestsPerSecond float64
	Burst             int
}

type MetricsConfig struct {
	Enabled bool
	Path    string
}

type CORSConfig struct {
	AllowedOrigins   []string
	AllowCredentials bool
}

type ServerConfig struct {
	Environment       string
	Host              string
	Port              int
	ShutdownTimeout   time.Duration
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	TrustProxyHeaders bool
	MaxJSONBodyBytes  int64
	MaxUploadBytes    int64
}

type DatabaseConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	MigrationsPath  string
}

type S3Config struct {
	Backend      string
	Endpoint     string
	AccessKey    string
	SecretKey    string
	Bucket       string
	UseSSL       bool
	Region       string
	CreateBucket bool
}

type AuthConfig struct {
	AccessTokenSecret  string
	AccessTokenExpiry  time.Duration
	RefreshTokenExpiry time.Duration
	Issuer             string
	RegistrationMode   string
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/cortex-sync/")

	v.SetEnvPrefix("CORTEX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)
	_ = v.ReadInConfig()

	storage, err := resolveStorageConfig(v)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{
			Environment:       strings.ToLower(strings.TrimSpace(v.GetString("server.env"))),
			Host:              v.GetString("server.host"),
			Port:              v.GetInt("server.port"),
			ShutdownTimeout:   v.GetDuration("server.shutdown_timeout"),
			ReadTimeout:       v.GetDuration("server.read_timeout"),
			ReadHeaderTimeout: v.GetDuration("server.read_header_timeout"),
			IdleTimeout:       v.GetDuration("server.idle_timeout"),
			TrustProxyHeaders: v.GetBool("server.trust_proxy_headers"),
			MaxJSONBodyBytes:  v.GetInt64("server.max_json_body"),
			MaxUploadBytes:    v.GetInt64("server.max_upload_body"),
		},
		Database: DatabaseConfig{
			URL:             v.GetString("database.url"),
			MaxConns:        int32(v.GetInt("database.max_conns")),
			MinConns:        int32(v.GetInt("database.min_conns")),
			MaxConnLifetime: v.GetDuration("database.max_conn_lifetime"),
			MaxConnIdleTime: v.GetDuration("database.max_conn_idle_time"),
			MigrationsPath:  v.GetString("database.migrations_path"),
		},
		S3: storage,
		Auth: AuthConfig{
			AccessTokenSecret:  v.GetString("auth.access_token_secret"),
			AccessTokenExpiry:  v.GetDuration("auth.access_token_expiry"),
			RefreshTokenExpiry: v.GetDuration("auth.refresh_token_expiry"),
			Issuer:             v.GetString("auth.issuer"),
			RegistrationMode:   strings.ToLower(strings.TrimSpace(v.GetString("auth.registration_mode"))),
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: v.GetFloat64("rate_limit.requests_per_second"),
			Burst:             v.GetInt("rate_limit.burst"),
		},
		Metrics: MetricsConfig{
			Enabled: v.GetBool("metrics.enabled"),
			Path:    v.GetString("metrics.path"),
		},
		CORS: CORSConfig{
			AllowedOrigins:   getStringList(v, "cors.allowed_origins"),
			AllowCredentials: v.GetBool("cors.allow_credentials"),
		},
		Sync: SyncConfig{
			MaxDeltasBeforeSnapshot: v.GetInt("sync.max_deltas_before_snapshot"),
			MaxDeltaSizeRatio:       v.GetFloat64("sync.max_delta_size_ratio"),
			MaxFileSize:             v.GetInt64("sync.max_file_size"),
			MaxSnapshotsPerFile:     v.GetInt("sync.max_snapshots_per_file"),
			EventRetention:          v.GetDuration("sync.event_retention"),
		},
		Collab: CollabConfig{
			MaxPeersPerRoom: v.GetInt("collab.max_peers_per_room"),
			FlushInterval:   v.GetDuration("collab.flush_interval"),
			MaxBufBytes:     v.GetInt("collab.max_buf_bytes"),
		},
		Subscription: SubscriptionConfig{
			Enabled:             v.GetBool("subscription.enabled"),
			StripeSecretKey:     v.GetString("subscription.stripe_secret_key"),
			StripePriceID:       v.GetString("subscription.stripe_price_id"),
			StripeWebhookSecret: v.GetString("subscription.stripe_webhook_secret"),
			CacheTTL:            v.GetDuration("subscription.cache_ttl"),
			RenewalGrace:        v.GetDuration("subscription.renewal_grace"),
		},
		DevOps: DevOpsConfig{
			DiscordWebhookURL: strings.TrimSpace(v.GetString("devops.discord_webhook_url")),
			DiscordUsername:   strings.TrimSpace(v.GetString("devops.discord_username")),
			DiscordTimeout:    v.GetDuration("devops.discord_timeout"),
		},
		UseFakeRepos: v.GetBool("use_fake_repos"),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.env", "development")
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.shutdown_timeout", 15*time.Second)
	v.SetDefault("server.read_timeout", 15*time.Second)
	v.SetDefault("server.read_header_timeout", 5*time.Second)
	v.SetDefault("server.idle_timeout", 60*time.Second)
	v.SetDefault("server.trust_proxy_headers", false)
	v.SetDefault("server.max_json_body", 134217728)
	v.SetDefault("server.max_upload_body", 104857600)

	v.SetDefault("database.url", "postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable")
	v.SetDefault("database.max_conns", 25)
	v.SetDefault("database.min_conns", 5)
	v.SetDefault("database.max_conn_lifetime", time.Hour)
	v.SetDefault("database.max_conn_idle_time", 30*time.Minute)
	v.SetDefault("database.migrations_path", "file://migrations")

	v.SetDefault("auth.access_token_secret", "change-me-in-production")
	v.SetDefault("auth.access_token_expiry", 15*time.Minute)
	v.SetDefault("auth.refresh_token_expiry", 90*24*time.Hour)
	v.SetDefault("auth.issuer", "cortex-sync")
	v.SetDefault("auth.registration_mode", "open")

	v.SetDefault("rate_limit.requests_per_second", 100.0)
	v.SetDefault("rate_limit.burst", 200)

	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.path", "/metrics")

	v.SetDefault("cors.allowed_origins", "*")
	v.SetDefault("cors.allow_credentials", false)

	v.SetDefault("sync.max_deltas_before_snapshot", 10)
	v.SetDefault("sync.max_delta_size_ratio", 0.5)
	v.SetDefault("sync.max_file_size", 104857600)
	v.SetDefault("sync.max_snapshots_per_file", 50)
	v.SetDefault("sync.event_retention", 30*24*time.Hour)

	v.SetDefault("collab.max_peers_per_room", 10)
	v.SetDefault("collab.flush_interval", 10*time.Second)
	v.SetDefault("collab.max_buf_bytes", 4*1024*1024)

	v.SetDefault("subscription.enabled", false)
	v.SetDefault("subscription.stripe_secret_key", "")
	v.SetDefault("subscription.stripe_price_id", "")
	v.SetDefault("subscription.stripe_webhook_secret", "")
	v.SetDefault("subscription.cache_ttl", time.Minute)
	v.SetDefault("subscription.renewal_grace", 48*time.Hour)

	v.SetDefault("devops.discord_webhook_url", "")
	v.SetDefault("devops.discord_username", "Cortex DevOps")
	v.SetDefault("devops.discord_timeout", 5*time.Second)

	v.SetDefault("use_fake_repos", false)
}

func resolveStorageConfig(v *viper.Viper) (S3Config, error) {
	legacyProvider := strings.ToLower(strings.TrimSpace(v.GetString("s3.provider")))
	backend, err := normalizeStorageBackend(v.GetString("storage.backend"))
	if err != nil {
		return S3Config{}, err
	}
	if backend == "" {
		backend, err = storageBackendFromMode(v.GetString("storage.mode"), v.GetString("storage.remote_provider"))
		if err != nil {
			return S3Config{}, err
		}
	}
	if backend == "" {
		backend, err = storageBackendFromLegacyProvider(legacyProvider)
		if err != nil {
			return S3Config{}, err
		}
	}
	if backend == "" {
		backend = "local"
	}

	endpoint := getStorageValue(v, "storage.endpoint", "s3.endpoint")
	accessKey := getStorageValue(v, "storage.access_key", "s3.access_key")
	secretKey := getStorageValue(v, "storage.secret_key", "s3.secret_key")
	bucket := getStorageValue(v, "storage.bucket", "s3.bucket")
	region := getStorageValue(v, "storage.region", "s3.region")

	if endpoint == "" && backend == "local" {
		endpoint = "localhost:9000"
	}
	if accessKey == "" && backend == "local" {
		accessKey = "minioadmin"
	}
	if secretKey == "" && backend == "local" {
		secretKey = "minioadmin"
	}
	if bucket == "" {
		bucket = "cortex-snapshots"
	}
	if region == "" {
		if backend == "r2" {
			region = "auto"
		} else {
			region = "us-east-1"
		}
	}

	normalizedEndpoint, endpointUseSSL, err := normalizeStorageEndpoint(endpoint)
	if err != nil {
		return S3Config{}, err
	}
	useSSL := backend != "local"
	if backend == "local" {
		useSSL = false
	}
	if legacyUseSSL := strings.TrimSpace(v.GetString("s3.use_ssl")); legacyUseSSL != "" {
		useSSL = v.GetBool("s3.use_ssl")
	}
	if endpointUseSSL != nil {
		useSSL = *endpointUseSSL
	}

	createBucket := backend == "local"
	if legacyCreateBucket := strings.TrimSpace(v.GetString("s3.create_bucket")); legacyCreateBucket != "" {
		createBucket = v.GetBool("s3.create_bucket")
	}

	return S3Config{
		Backend:      backend,
		Endpoint:     normalizedEndpoint,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Bucket:       bucket,
		UseSSL:       useSSL,
		Region:       region,
		CreateBucket: createBucket,
	}, nil
}

func normalizeStorageBackend(backend string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(backend))
	if normalized == "" {
		return "", nil
	}
	if normalized == "local" || normalized == "s3" || normalized == "r2" {
		return normalized, nil
	}
	return "", fmt.Errorf("unsupported storage backend %q", backend)
}

func storageBackendFromMode(mode string, remoteProvider string) (string, error) {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if normalizedMode == "" {
		return "", nil
	}
	if normalizedMode == "local" {
		return "local", nil
	}
	if normalizedMode != "remote" {
		return "", fmt.Errorf("unsupported storage mode %q", mode)
	}

	normalizedRemoteProvider := strings.ToLower(strings.TrimSpace(remoteProvider))
	if normalizedRemoteProvider == "" {
		return "r2", nil
	}
	if normalizedRemoteProvider == "s3" || normalizedRemoteProvider == "r2" {
		return normalizedRemoteProvider, nil
	}
	return "", fmt.Errorf("unsupported remote storage provider %q", remoteProvider)
}

func storageBackendFromLegacyProvider(provider string) (string, error) {
	switch provider {
	case "":
		return "", nil
	case "minio":
		return "local", nil
	case "custom", "s3":
		return "s3", nil
	case "r2":
		return "r2", nil
	case "localstack":
		return "", fmt.Errorf("legacy s3 provider localstack is no longer supported; use CORTEX_STORAGE_BACKEND=local")
	default:
		return "", fmt.Errorf("unsupported legacy s3 provider %q", provider)
	}
}

func getStorageValue(v *viper.Viper, storageKey string, legacyKey string) string {
	if value := strings.TrimSpace(v.GetString(storageKey)); value != "" {
		return value
	}
	return strings.TrimSpace(v.GetString(legacyKey))
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

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func getStringList(v *viper.Viper, key string) []string {
	values := v.GetStringSlice(key)
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 {
		return splitCSV(values[0])
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, splitCSV(value)...)
	}
	return result
}

func (cfg *Config) Validate() error {
	if cfg.Server.Environment == "" {
		cfg.Server.Environment = "development"
	}
	if cfg.Auth.RegistrationMode == "" {
		cfg.Auth.RegistrationMode = "open"
	}
	if cfg.Auth.RegistrationMode != "open" && cfg.Auth.RegistrationMode != "first-user" && cfg.Auth.RegistrationMode != "closed" {
		return fmt.Errorf("auth registration mode must be open, first-user, or closed")
	}
	if cfg.DevOps.DiscordUsername == "" {
		cfg.DevOps.DiscordUsername = "Cortex DevOps"
	}
	if cfg.DevOps.DiscordTimeout <= 0 {
		return fmt.Errorf("devops discord timeout must be positive")
	}
	if cfg.Server.MaxJSONBodyBytes < 0 {
		return fmt.Errorf("server max json body must be non-negative")
	}
	if cfg.Server.MaxUploadBytes < 0 {
		return fmt.Errorf("server max upload body must be non-negative")
	}
	if cfg.S3.Endpoint == "" {
		return fmt.Errorf("storage endpoint is required")
	}
	if cfg.S3.Bucket == "" {
		return fmt.Errorf("storage bucket is required")
	}
	if cfg.S3.Backend != "local" && (cfg.S3.AccessKey == "" || cfg.S3.SecretKey == "") {
		return fmt.Errorf("remote storage credentials are required")
	}
	if cfg.S3.Backend != "local" && cfg.S3.CreateBucket {
		return fmt.Errorf("remote storage bucket creation is disabled; create the bucket before deploying")
	}
	if cfg.S3.Backend == "r2" {
		if !strings.HasSuffix(cfg.S3.Endpoint, ".r2.cloudflarestorage.com") {
			return fmt.Errorf("r2 endpoint must end with .r2.cloudflarestorage.com")
		}
		if cfg.S3.Region == "" {
			cfg.S3.Region = "auto"
		}
		if cfg.S3.Region != "auto" && cfg.S3.Region != "us-east-1" {
			return fmt.Errorf("r2 region must be auto or us-east-1")
		}
		if !cfg.S3.UseSSL {
			return fmt.Errorf("r2 requires ssl")
		}
		if cfg.S3.CreateBucket {
			return fmt.Errorf("r2 bucket creation must be disabled; create the bucket in Cloudflare first")
		}
	}
	if cfg.Server.Environment == "production" {
		if cfg.Auth.AccessTokenSecret == "" || cfg.Auth.AccessTokenSecret == "change-me-in-production" || len(cfg.Auth.AccessTokenSecret) < 32 {
			return fmt.Errorf("production auth access token secret must be at least 32 characters and not use the default value")
		}
		if cfg.Database.URL == "postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable" {
			return fmt.Errorf("production database url must not use the development default")
		}
		if cfg.UseFakeRepos {
			return fmt.Errorf("production cannot use fake repositories")
		}
		if cfg.Subscription.Enabled {
			if cfg.Subscription.StripeSecretKey == "" {
				return fmt.Errorf("production subscription stripe secret key is required when subscription is enabled")
			}
			if cfg.Subscription.StripePriceID == "" {
				return fmt.Errorf("production subscription stripe price id is required when subscription is enabled")
			}
			if cfg.Subscription.StripeWebhookSecret == "" {
				return fmt.Errorf("production subscription stripe webhook secret is required when subscription is enabled")
			}
		}
		if cfg.DevOps.DiscordWebhookURL != "" && !strings.HasPrefix(cfg.DevOps.DiscordWebhookURL, "https://discord.com/api/webhooks/") {
			return fmt.Errorf("production discord webhook url must use https://discord.com/api/webhooks/")
		}
	}

	return nil
}
