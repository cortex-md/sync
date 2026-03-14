package config

import (
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
	Sync         SyncConfig
	Collab       CollabConfig
	UseFakeRepos bool
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

type ServerConfig struct {
	Host            string
	Port            int
	ShutdownTimeout time.Duration
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
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string
}

type AuthConfig struct {
	AccessTokenSecret  string
	AccessTokenExpiry  time.Duration
	RefreshTokenExpiry time.Duration
	Issuer             string
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

	cfg := &Config{
		Server: ServerConfig{
			Host:            v.GetString("server.host"),
			Port:            v.GetInt("server.port"),
			ShutdownTimeout: v.GetDuration("server.shutdown_timeout"),
		},
		Database: DatabaseConfig{
			URL:             v.GetString("database.url"),
			MaxConns:        int32(v.GetInt("database.max_conns")),
			MinConns:        int32(v.GetInt("database.min_conns")),
			MaxConnLifetime: v.GetDuration("database.max_conn_lifetime"),
			MaxConnIdleTime: v.GetDuration("database.max_conn_idle_time"),
			MigrationsPath:  v.GetString("database.migrations_path"),
		},
		S3: S3Config{
			Endpoint:  v.GetString("s3.endpoint"),
			AccessKey: v.GetString("s3.access_key"),
			SecretKey: v.GetString("s3.secret_key"),
			Bucket:    v.GetString("s3.bucket"),
			UseSSL:    v.GetBool("s3.use_ssl"),
			Region:    v.GetString("s3.region"),
		},
		Auth: AuthConfig{
			AccessTokenSecret:  v.GetString("auth.access_token_secret"),
			AccessTokenExpiry:  v.GetDuration("auth.access_token_expiry"),
			RefreshTokenExpiry: v.GetDuration("auth.refresh_token_expiry"),
			Issuer:             v.GetString("auth.issuer"),
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: v.GetFloat64("rate_limit.requests_per_second"),
			Burst:             v.GetInt("rate_limit.burst"),
		},
		Metrics: MetricsConfig{
			Enabled: v.GetBool("metrics.enabled"),
			Path:    v.GetString("metrics.path"),
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
		UseFakeRepos: v.GetBool("use_fake_repos"),
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.shutdown_timeout", 15*time.Second)

	v.SetDefault("database.url", "postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable")
	v.SetDefault("database.max_conns", 25)
	v.SetDefault("database.min_conns", 5)
	v.SetDefault("database.max_conn_lifetime", time.Hour)
	v.SetDefault("database.max_conn_idle_time", 30*time.Minute)
	v.SetDefault("database.migrations_path", "file://migrations")

	v.SetDefault("s3.endpoint", "localhost:9000")
	v.SetDefault("s3.access_key", "minioadmin")
	v.SetDefault("s3.secret_key", "minioadmin")
	v.SetDefault("s3.bucket", "cortex-snapshots")
	v.SetDefault("s3.use_ssl", false)
	v.SetDefault("s3.region", "us-east-1")

	v.SetDefault("auth.access_token_secret", "change-me-in-production")
	v.SetDefault("auth.access_token_expiry", 15*time.Minute)
	v.SetDefault("auth.refresh_token_expiry", 90*24*time.Hour)
	v.SetDefault("auth.issuer", "cortex-sync")

	v.SetDefault("rate_limit.requests_per_second", 100.0)
	v.SetDefault("rate_limit.burst", 200)

	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.path", "/metrics")

	v.SetDefault("sync.max_deltas_before_snapshot", 10)
	v.SetDefault("sync.max_delta_size_ratio", 0.5)
	v.SetDefault("sync.max_file_size", 104857600)
	v.SetDefault("sync.max_snapshots_per_file", 50)
	v.SetDefault("sync.event_retention", 30*24*time.Hour)

	v.SetDefault("collab.max_peers_per_room", 10)
	v.SetDefault("collab.flush_interval", 10*time.Second)
	v.SetDefault("collab.max_buf_bytes", 4*1024*1024)

	v.SetDefault("use_fake_repos", false)
}
