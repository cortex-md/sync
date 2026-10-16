package config

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Server       ServerConfig       `envPrefix:"SERVER_"`
	Database     DatabaseConfig     `envPrefix:"DATABASE_"`
	S3           S3Config           `env:"-"`
	Auth         AuthConfig         `envPrefix:"AUTH_"`
	RateLimit    RateLimitConfig    `envPrefix:"RATE_LIMIT_"`
	Metrics      MetricsConfig      `envPrefix:"METRICS_"`
	CORS         CORSConfig         `envPrefix:"CORS_"`
	Sync         SyncConfig         `envPrefix:"SYNC_"`
	Collab       CollabConfig       `envPrefix:"COLLAB_"`
	Subscription SubscriptionConfig `envPrefix:"SUBSCRIPTION_"`
	DevOps       DevOpsConfig       `envPrefix:"DEVOPS_"`
	Mail         MailConfig         `envPrefix:"MAIL_"`
	Avatar       AvatarConfig       `envPrefix:"AVATAR_"`
	Feature      FeatureConfig      `envPrefix:"FEATURE_"`
	Jobs         JobsConfig         `envPrefix:"JOBS_"`
	Tenancy      TenancyConfig
	Protection   ProtectionConfig `envPrefix:"PROTECTION_"`
	Deployment   string           `env:"DEPLOYMENT" envDefault:"single"`
	ProxyRaw     string           `env:"PROXY"`
	Proxy        ProxyConfig      `env:"-"`
	Bus          string           `env:"-"`
	FeaturesRaw  []string         `env:"FEATURES" envSeparator:","`
	UseFakeRepos bool             `env:"USE_FAKE_REPOS"`
	Warnings     []string         `env:"-"`
}

type ServerConfig struct {
	Environment       string        `env:"ENV" envDefault:"development"`
	Host              string        `env:"HOST" envDefault:"0.0.0.0"`
	Port              int           `env:"PORT" envDefault:"8080"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT" envDefault:"15s"`
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`
	IdleTimeout       time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
	TrustProxyHeaders bool          `env:"TRUST_PROXY_HEADERS"`
	MaxJSONBodyBytes  int64         `env:"MAX_JSON_BODY"`
	MaxUploadBytes    int64         `env:"MAX_UPLOAD_BODY"`
	PublicURL         string        `env:"PUBLIC_URL"`
	DrainDelay        time.Duration `env:"DRAIN_DELAY" envDefault:"0s"`
}

type DatabaseConfig struct {
	URL             string        `env:"URL" envDefault:"postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable"`
	MaxConns        int32         `env:"MAX_CONNS"`
	MinConns        int32         `env:"MIN_CONNS"`
	MaxConnLifetime time.Duration `env:"MAX_CONN_LIFETIME" envDefault:"1h"`
	MaxConnIdleTime time.Duration `env:"MAX_CONN_IDLE_TIME"`
	MigrationsPath  string        `env:"MIGRATIONS_PATH"`
	MigrateOnStart  bool          `env:"MIGRATE_ON_START"`
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
	AccessTokenSecret  string        `env:"ACCESS_TOKEN_SECRET" envDefault:"change-me-in-production"`
	AccessTokenExpiry  time.Duration `env:"ACCESS_TOKEN_EXPIRY" envDefault:"15m"`
	RefreshTokenExpiry time.Duration `env:"REFRESH_TOKEN_EXPIRY" envDefault:"2160h"`
	Issuer             string        `env:"ISSUER" envDefault:"cortex-sync"`
	RegistrationMode   string        `env:"REGISTRATION_MODE" envDefault:"open"`
}

type RateLimitConfig struct {
	RequestsPerSecond          float64 `env:"REQUESTS_PER_SECOND"`
	Burst                      int     `env:"BURST"`
	SensitiveRequestsPerSecond float64 `env:"SENSITIVE_REQUESTS_PER_SECOND"`
	SensitiveBurst             int     `env:"SENSITIVE_BURST"`
}

type MetricsConfig struct {
	Enabled bool   `env:"ENABLED"`
	Path    string `env:"PATH" envDefault:"/metrics"`
}

type CORSConfig struct {
	AllowedOrigins   []string `env:"ALLOWED_ORIGINS" envDefault:"*" envSeparator:","`
	AllowCredentials bool     `env:"ALLOW_CREDENTIALS"`
}

type SyncConfig struct {
	MaxDeltasBeforeSnapshot int           `env:"MAX_DELTAS_BEFORE_SNAPSHOT" envDefault:"10"`
	MaxDeltaSizeRatio       float64       `env:"MAX_DELTA_SIZE_RATIO" envDefault:"0.5"`
	MaxFileSize             int64         `env:"MAX_FILE_SIZE"`
	MaxSnapshotsPerFile     int           `env:"MAX_SNAPSHOTS_PER_FILE" envDefault:"50"`
	EventRetention          time.Duration `env:"EVENT_RETENTION" envDefault:"720h"`
}

type CollabConfig struct {
	MaxPeersPerRoom int           `env:"MAX_PEERS_PER_ROOM" envDefault:"10"`
	FlushInterval   time.Duration `env:"FLUSH_INTERVAL" envDefault:"2s"`
	MaxBufBytes     int           `env:"MAX_BUF_BYTES" envDefault:"4194304"`
}

type SubscriptionConfig struct {
	Enabled             bool          `env:"ENABLED"`
	StripeSecretKey     string        `env:"STRIPE_SECRET_KEY"`
	StripePriceID       string        `env:"STRIPE_PRICE_ID"`
	StripeWebhookSecret string        `env:"STRIPE_WEBHOOK_SECRET"`
	CacheTTL            time.Duration `env:"CACHE_TTL" envDefault:"60s"`
	RenewalGrace        time.Duration `env:"RENEWAL_GRACE" envDefault:"48h"`
}

type DevOpsConfig struct {
	DiscordWebhookURL string        `env:"DISCORD_WEBHOOK_URL"`
	DiscordUsername   string        `env:"DISCORD_USERNAME" envDefault:"Cortex DevOps"`
	DiscordTimeout    time.Duration `env:"DISCORD_TIMEOUT" envDefault:"5s"`
}

type MailConfig struct {
	Driver       string        `env:"DRIVER" envDefault:"none"`
	ResendAPIKey string        `env:"RESEND_API_KEY"`
	ResendURL    string        `env:"RESEND_URL" envDefault:"https://api.resend.com/emails"`
	Host         string        `env:"HOST"`
	Port         int           `env:"PORT" envDefault:"587"`
	Username     string        `env:"USERNAME"`
	Password     string        `env:"PASSWORD"`
	From         string        `env:"FROM" envDefault:"Cortex <no-reply@localhost>"`
	TLSMode      string        `env:"TLS_MODE" envDefault:"starttls"`
	Timeout      time.Duration `env:"TIMEOUT" envDefault:"15s"`
}

type AvatarConfig struct {
	MaxBytes int64 `env:"MAX_BYTES" envDefault:"5242880"`
	Size     int   `env:"SIZE" envDefault:"256"`
}

type FeatureConfig struct {
	Collab               bool   `env:"COLLAB"`
	Avatars              bool   `env:"AVATARS"`
	Guests               bool   `env:"GUESTS"`
	PublicLinks          bool   `env:"PUBLIC_LINKS"`
	EmailVerificationRaw string `env:"EMAIL_VERIFICATION" envDefault:"auto"`
	PasswordResetRaw     string `env:"PASSWORD_RESET" envDefault:"auto"`
	EmailVerification    bool   `env:"-"`
	PasswordReset        bool   `env:"-"`
}

type JobsConfig struct {
	SweepInterval time.Duration `env:"SWEEP_INTERVAL" envDefault:"1h"`
}

type TenancyConfig struct {
	Mode                 string `env:"TENANCY_MODE" envDefault:"single"`
	VaultCreation        string `env:"VAULT_CREATION" envDefault:"everyone"`
	DefaultVaultQuotaRaw string `env:"TENANCY_DEFAULT_VAULT_QUOTA"`
	DefaultVaultQuota    int64  `env:"-"`
	MaxVaultsPerUser     int    `env:"TENANCY_MAX_VAULTS_PER_USER"`
	MaxDevicesPerUser    int    `env:"TENANCY_MAX_DEVICES_PER_USER"`
	MaxPendingInvites    int    `env:"TENANCY_MAX_PENDING_INVITES_PER_VAULT"`
	MaxSharesPerVault    int    `env:"TENANCY_MAX_SHARES_PER_VAULT"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.ParseWithOptions(cfg, env.Options{Prefix: "CORTEX_"}); err != nil {
		return nil, fmt.Errorf("parsing environment: %w", err)
	}

	storage, err := resolveStorageConfig()
	if err != nil {
		return nil, err
	}
	cfg.S3 = storage

	normalize(cfg)
	if err := applyProfiles(cfg, os.LookupEnv); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func normalize(cfg *Config) {
	cfg.Server.Environment = strings.ToLower(strings.TrimSpace(cfg.Server.Environment))
	cfg.Server.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.Server.PublicURL), "/")
	cfg.Auth.RegistrationMode = strings.ToLower(strings.TrimSpace(cfg.Auth.RegistrationMode))
	cfg.DevOps.DiscordWebhookURL = strings.TrimSpace(cfg.DevOps.DiscordWebhookURL)
	cfg.DevOps.DiscordUsername = strings.TrimSpace(cfg.DevOps.DiscordUsername)
	cfg.Mail.Driver = strings.ToLower(strings.TrimSpace(cfg.Mail.Driver))
	cfg.Mail.Host = strings.TrimSpace(cfg.Mail.Host)
	cfg.Mail.From = strings.TrimSpace(cfg.Mail.From)
	cfg.Mail.TLSMode = strings.ToLower(strings.TrimSpace(cfg.Mail.TLSMode))
	cfg.Tenancy.Mode = strings.ToLower(strings.TrimSpace(cfg.Tenancy.Mode))
	cfg.Tenancy.VaultCreation = strings.ToLower(strings.TrimSpace(cfg.Tenancy.VaultCreation))
	origins := make([]string, 0, len(cfg.CORS.AllowedOrigins))
	for _, origin := range cfg.CORS.AllowedOrigins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	cfg.CORS.AllowedOrigins = origins
}

func (cfg *Config) MailEnabled() bool {
	return cfg.Mail.Driver != "none"
}

func (cfg *Config) resolveFeatures() error {
	verification, err := resolveMailFeature("CORTEX_FEATURE_EMAIL_VERIFICATION", cfg.Feature.EmailVerificationRaw, cfg.MailEnabled())
	if err != nil {
		return err
	}
	reset, err := resolveMailFeature("CORTEX_FEATURE_PASSWORD_RESET", cfg.Feature.PasswordResetRaw, cfg.MailEnabled())
	if err != nil {
		return err
	}
	cfg.Feature.EmailVerification = verification
	cfg.Feature.PasswordReset = reset

	if cfg.Tenancy.Mode != "single" && cfg.Tenancy.Mode != "multi" {
		return fmt.Errorf("CORTEX_TENANCY_MODE must be single or multi")
	}
	if cfg.Tenancy.VaultCreation != "everyone" && cfg.Tenancy.VaultCreation != "admins" {
		return fmt.Errorf("CORTEX_VAULT_CREATION must be everyone or admins")
	}
	return nil
}

func resolveMailFeature(name string, raw string, mailEnabled bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return mailEnabled, nil
	case "false", "0", "off":
		return false, nil
	case "true", "1", "on":
		if !mailEnabled {
			return false, fmt.Errorf("%s requires CORTEX_MAIL_DRIVER to be log, smtp, or resend", name)
		}
		return true, nil
	default:
		return false, fmt.Errorf("%s must be auto, true, or false", name)
	}
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
	if cfg.Server.PublicURL == "" {
		cfg.Server.PublicURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	}
	if _, err := url.ParseRequestURI(cfg.Server.PublicURL); err != nil {
		return fmt.Errorf("server public url is invalid: %w", err)
	}
	switch cfg.Mail.Driver {
	case "none", "log":
	case "resend":
		if strings.TrimSpace(cfg.Mail.ResendAPIKey) == "" || cfg.Mail.From == "" {
			return fmt.Errorf("resend mail driver requires CORTEX_MAIL_RESEND_API_KEY and CORTEX_MAIL_FROM")
		}
	case "smtp":
		if cfg.Mail.Host == "" || cfg.Mail.Port <= 0 || cfg.Mail.From == "" {
			return fmt.Errorf("smtp mail driver requires host, port and from")
		}
		if cfg.Mail.TLSMode != "starttls" && cfg.Mail.TLSMode != "tls" && cfg.Mail.TLSMode != "none" {
			return fmt.Errorf("mail tls mode must be starttls, tls, or none")
		}
	default:
		return fmt.Errorf("mail driver must be none, log, smtp, or resend")
	}
	if err := cfg.resolveFeatures(); err != nil {
		return err
	}
	if cfg.Jobs.SweepInterval < time.Second {
		return fmt.Errorf("CORTEX_JOBS_SWEEP_INTERVAL must be at least 1s")
	}
	if cfg.Avatar.MaxBytes <= 0 || cfg.Avatar.Size < 32 || cfg.Avatar.Size > 1024 {
		return fmt.Errorf("avatar max bytes must be positive and size between 32 and 1024")
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
	if err := cfg.validateSecret(); err != nil {
		return err
	}
	if cfg.CORS.AllowCredentials && slices.Contains(cfg.CORS.AllowedOrigins, "*") {
		return fmt.Errorf("CORTEX_CORS_ALLOW_CREDENTIALS requires explicit CORTEX_CORS_ALLOWED_ORIGINS instead of *")
	}
	if cfg.Server.Environment == "production" {
		if cfg.Database.URL == "postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable" {
			return fmt.Errorf("production database url must not use the development default")
		}
		if cfg.UseFakeRepos {
			return fmt.Errorf("production cannot use fake repositories")
		}
		if strings.HasPrefix(cfg.Server.PublicURL, "http://localhost") {
			return fmt.Errorf("production requires CORTEX_SERVER_PUBLIC_URL")
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

var knownSecrets = []string{"change-me-in-production", "dev-only-change-this-token-secret-32"}

func (cfg *Config) validateSecret() error {
	secret := cfg.Auth.AccessTokenSecret
	weak := len(secret) < 32 || slices.Contains(knownSecrets, secret)
	if !weak {
		return nil
	}
	if cfg.Server.Environment != "production" && (cfg.UseFakeRepos || isLocalURL(cfg.Server.PublicURL)) {
		cfg.Warnings = append(cfg.Warnings, "CORTEX_AUTH_ACCESS_TOKEN_SECRET is a known or short value; generate one with `openssl rand -hex 32` before exposing this server")
		return nil
	}
	return fmt.Errorf("CORTEX_AUTH_ACCESS_TOKEN_SECRET must be at least 32 random characters and not a published default (generate one with `openssl rand -hex 32`)")
}

func isLocalURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
