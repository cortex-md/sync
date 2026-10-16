package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	DeploymentSingle  = "single"
	DeploymentCluster = "cluster"

	BusMemory   = "memory"
	BusPostgres = "postgres"

	ProxyNone       = "none"
	ProxyHops       = "hops"
	ProxyCloudflare = "cloudflare"

	ProtectionOff      = "off"
	ProtectionStandard = "standard"
	ProtectionStrict   = "strict"
)

var knownFeatures = []string{"collab", "avatars", "guests", "public_links", "metrics"}

type ProxyConfig struct {
	Mode string
	Hops int
}

type ProtectionConfig struct {
	Level                       string  `env:"-"`
	RegistrationsPerHour        int     `env:"REGISTRATIONS_PER_HOUR"`
	UserReadPerSecond           float64 `env:"USER_READ_PER_SECOND"`
	UserReadBurst               int     `env:"USER_READ_BURST"`
	UserWritePerSecond          float64 `env:"USER_WRITE_PER_SECOND"`
	UserWriteBurst              int     `env:"USER_WRITE_BURST"`
	UserHeavyPerSecond          float64 `env:"USER_HEAVY_PER_SECOND"`
	UserHeavyBurst              int     `env:"USER_HEAVY_BURST"`
	PublicPerSecond             float64 `env:"PUBLIC_PER_SECOND"`
	PublicBurst                 int     `env:"PUBLIC_BURST"`
	MaxConcurrentUploads        int     `env:"MAX_CONCURRENT_UPLOADS"`
	MaxConcurrentUploadsPerUser int     `env:"MAX_CONCURRENT_UPLOADS_PER_USER"`
	MaxConcurrentBatchesPerUser int     `env:"MAX_CONCURRENT_BATCHES_PER_USER"`
	UploadBytesPerHour          int64   `env:"UPLOAD_BYTES_PER_HOUR"`
	MaxDeltaBytes               int64   `env:"MAX_DELTA_BYTES"`
	MaxSSEPerUser               int     `env:"MAX_SSE_PER_USER"`
	MaxSSETotal                 int     `env:"MAX_SSE_TOTAL"`
	MaxCollabPerUser            int     `env:"MAX_COLLAB_PER_USER"`
	CollabMessagesPerSecond     float64 `env:"COLLAB_MESSAGES_PER_SECOND"`
	CollabMessagesBurst         int     `env:"COLLAB_MESSAGES_BURST"`
	LoginFailuresBeforeLockout  int     `env:"LOGIN_FAILURES_BEFORE_LOCKOUT"`
	MaxConcurrentPasswordHashes int     `env:"MAX_CONCURRENT_PASSWORD_HASHES"`
	RateLimitMaxKeys            int     `env:"RATE_LIMIT_MAX_KEYS"`
}

func (p ProtectionConfig) Enabled() bool {
	return p.Level != ProtectionOff
}

type lookupFunc func(string) (string, bool)

type preset struct {
	lookup lookupFunc
}

func (p preset) explicit(name string) bool {
	_, ok := p.lookup("CORTEX_" + name)
	return ok
}

func setInt[T int | int32 | int64](p preset, name string, target *T, value T) {
	if !p.explicit(name) {
		*target = value
	}
}

func setFloat(p preset, name string, target *float64, value float64) {
	if !p.explicit(name) {
		*target = value
	}
}

func setDuration(p preset, name string, target *time.Duration, value time.Duration) {
	if !p.explicit(name) {
		*target = value
	}
}

func setBool(p preset, name string, target *bool, value bool) {
	if !p.explicit(name) {
		*target = value
	}
}

func applyProfiles(cfg *Config, lookup lookupFunc) error {
	p := preset{lookup: lookup}
	if port, ok := lookup("PORT"); ok && !p.explicit("SERVER_PORT") {
		parsed, err := strconv.Atoi(strings.TrimSpace(port))
		if err != nil || parsed <= 0 || parsed > 65535 {
			return fmt.Errorf("PORT must be a valid port number")
		}
		cfg.Server.Port = parsed
	}
	if err := applyDeployment(cfg, p); err != nil {
		return err
	}
	if err := applyProxy(cfg, p); err != nil {
		return err
	}
	if err := applyProtection(cfg, p); err != nil {
		return err
	}
	if err := applyFeatures(cfg, p); err != nil {
		return err
	}
	return applyTenancy(cfg, p)
}

func applyDeployment(cfg *Config, p preset) error {
	cfg.Deployment = strings.ToLower(strings.TrimSpace(cfg.Deployment))
	switch cfg.Deployment {
	case "", DeploymentSingle:
		cfg.Deployment = DeploymentSingle
		cfg.Bus = BusMemory
		setBool(p, "DATABASE_MIGRATE_ON_START", &cfg.Database.MigrateOnStart, true)
		setInt(p, "DATABASE_MAX_CONNS", &cfg.Database.MaxConns, 5)
		setInt(p, "DATABASE_MIN_CONNS", &cfg.Database.MinConns, 0)
		setDuration(p, "DATABASE_MAX_CONN_IDLE_TIME", &cfg.Database.MaxConnIdleTime, 2*time.Minute)
	case DeploymentCluster:
		cfg.Bus = BusPostgres
		if p.explicit("DATABASE_MIGRATE_ON_START") && cfg.Database.MigrateOnStart {
			cfg.Warnings = append(cfg.Warnings, "CORTEX_DATABASE_MIGRATE_ON_START with CORTEX_DEPLOYMENT=cluster makes every replica migrate on boot; prefer `cortex-sync migrate up` before rolling out")
		}
		setInt(p, "DATABASE_MAX_CONNS", &cfg.Database.MaxConns, 10)
		setInt(p, "DATABASE_MIN_CONNS", &cfg.Database.MinConns, 1)
		setDuration(p, "DATABASE_MAX_CONN_IDLE_TIME", &cfg.Database.MaxConnIdleTime, 30*time.Minute)
	default:
		return fmt.Errorf("CORTEX_DEPLOYMENT must be single or cluster")
	}
	if cfg.UseFakeRepos {
		cfg.Bus = BusMemory
		cfg.Database.MigrateOnStart = false
	}
	if cfg.Database.MinConns > cfg.Database.MaxConns {
		return fmt.Errorf("CORTEX_DATABASE_MIN_CONNS cannot exceed CORTEX_DATABASE_MAX_CONNS")
	}
	return nil
}

func applyProxy(cfg *Config, p preset) error {
	raw := strings.ToLower(strings.TrimSpace(cfg.ProxyRaw))
	if raw == "" && cfg.Server.TrustProxyHeaders {
		cfg.Warnings = append(cfg.Warnings, "CORTEX_SERVER_TRUST_PROXY_HEADERS is deprecated; use CORTEX_PROXY=hops:1 (or render, cloudflare)")
		raw = "hops:1"
	}
	switch {
	case raw == "" || raw == ProxyNone:
		cfg.Proxy = ProxyConfig{Mode: ProxyNone}
	case raw == "render":
		cfg.Proxy = ProxyConfig{Mode: ProxyHops, Hops: 1}
	case raw == ProxyCloudflare:
		cfg.Proxy = ProxyConfig{Mode: ProxyCloudflare}
	case strings.HasPrefix(raw, "hops:"):
		hops, err := strconv.Atoi(strings.TrimPrefix(raw, "hops:"))
		if err != nil || hops < 1 || hops > 10 {
			return fmt.Errorf("CORTEX_PROXY hops must be between 1 and 10")
		}
		cfg.Proxy = ProxyConfig{Mode: ProxyHops, Hops: hops}
	default:
		return fmt.Errorf("CORTEX_PROXY must be none, render, cloudflare or hops:N")
	}
	cfg.Server.TrustProxyHeaders = cfg.Proxy.Mode != ProxyNone
	return nil
}

type protectionValues struct {
	ipRate, ipBurst                  float64
	sensitiveRate, sensitiveBurst    float64
	registrations                    int
	readRate, readBurst              float64
	writeRate, writeBurst            float64
	heavyRate, heavyBurst            float64
	publicRate, publicBurst          float64
	uploads, uploadsPerUser, batches int
	uploadBytesPerHour               int64
	maxDelta                         int64
	ssePerUser, sseTotal, collabUser int
	collabRate, collabBurst          float64
	loginFailures, hashes, maxKeys   int
	maxJSON, maxUpload, maxFile      int64
}

const mib = int64(1) << 20

var protectionPresets = map[string]protectionValues{
	ProtectionOff: {
		maxKeys: 50000, maxDelta: 4 * mib, hashes: 4,
		maxJSON: 32 * mib, maxUpload: 100 * mib, maxFile: 100 * mib,
	},
	ProtectionStandard: {
		ipRate: 20, ipBurst: 60, sensitiveRate: 0.2, sensitiveBurst: 5, registrations: 5,
		readRate: 50, readBurst: 100, writeRate: 20, writeBurst: 60, heavyRate: 1, heavyBurst: 5,
		publicRate: 10, publicBurst: 30, uploads: 4, uploadsPerUser: 2, batches: 1,
		uploadBytesPerHour: 500 * mib, maxDelta: mib, ssePerUser: 8, sseTotal: 300, collabUser: 16,
		collabRate: 50, collabBurst: 200, loginFailures: 5, hashes: 2, maxKeys: 50000,
		maxJSON: 2 * mib, maxUpload: 25 * mib, maxFile: 25 * mib,
	},
	ProtectionStrict: {
		ipRate: 10, ipBurst: 30, sensitiveRate: 0.1, sensitiveBurst: 3, registrations: 2,
		readRate: 20, readBurst: 40, writeRate: 8, writeBurst: 20, heavyRate: 0.5, heavyBurst: 3,
		publicRate: 5, publicBurst: 15, uploads: 2, uploadsPerUser: 1, batches: 1,
		uploadBytesPerHour: 200 * mib, maxDelta: mib, ssePerUser: 4, sseTotal: 150, collabUser: 8,
		collabRate: 30, collabBurst: 100, loginFailures: 3, hashes: 1, maxKeys: 20000,
		maxJSON: mib, maxUpload: 10 * mib, maxFile: 10 * mib,
	},
}

func applyProtection(cfg *Config, p preset) error {
	level, _ := p.lookup("CORTEX_PROTECTION")
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		level = ProtectionStandard
	}
	values, ok := protectionPresets[level]
	if !ok {
		return fmt.Errorf("CORTEX_PROTECTION must be off, standard or strict")
	}
	pc := &cfg.Protection
	pc.Level = level

	setFloat(p, "RATE_LIMIT_REQUESTS_PER_SECOND", &cfg.RateLimit.RequestsPerSecond, values.ipRate)
	setInt(p, "RATE_LIMIT_BURST", &cfg.RateLimit.Burst, int(values.ipBurst))
	setFloat(p, "RATE_LIMIT_SENSITIVE_REQUESTS_PER_SECOND", &cfg.RateLimit.SensitiveRequestsPerSecond, values.sensitiveRate)
	setInt(p, "RATE_LIMIT_SENSITIVE_BURST", &cfg.RateLimit.SensitiveBurst, int(values.sensitiveBurst))
	setInt(p, "SERVER_MAX_JSON_BODY", &cfg.Server.MaxJSONBodyBytes, values.maxJSON)
	setInt(p, "SERVER_MAX_UPLOAD_BODY", &cfg.Server.MaxUploadBytes, values.maxUpload)
	setInt(p, "SYNC_MAX_FILE_SIZE", &cfg.Sync.MaxFileSize, values.maxFile)

	setInt(p, "PROTECTION_REGISTRATIONS_PER_HOUR", &pc.RegistrationsPerHour, values.registrations)
	setFloat(p, "PROTECTION_USER_READ_PER_SECOND", &pc.UserReadPerSecond, values.readRate)
	setInt(p, "PROTECTION_USER_READ_BURST", &pc.UserReadBurst, int(values.readBurst))
	setFloat(p, "PROTECTION_USER_WRITE_PER_SECOND", &pc.UserWritePerSecond, values.writeRate)
	setInt(p, "PROTECTION_USER_WRITE_BURST", &pc.UserWriteBurst, int(values.writeBurst))
	setFloat(p, "PROTECTION_USER_HEAVY_PER_SECOND", &pc.UserHeavyPerSecond, values.heavyRate)
	setInt(p, "PROTECTION_USER_HEAVY_BURST", &pc.UserHeavyBurst, int(values.heavyBurst))
	setFloat(p, "PROTECTION_PUBLIC_PER_SECOND", &pc.PublicPerSecond, values.publicRate)
	setInt(p, "PROTECTION_PUBLIC_BURST", &pc.PublicBurst, int(values.publicBurst))
	setInt(p, "PROTECTION_MAX_CONCURRENT_UPLOADS", &pc.MaxConcurrentUploads, values.uploads)
	setInt(p, "PROTECTION_MAX_CONCURRENT_UPLOADS_PER_USER", &pc.MaxConcurrentUploadsPerUser, values.uploadsPerUser)
	setInt(p, "PROTECTION_MAX_CONCURRENT_BATCHES_PER_USER", &pc.MaxConcurrentBatchesPerUser, values.batches)
	setInt(p, "PROTECTION_UPLOAD_BYTES_PER_HOUR", &pc.UploadBytesPerHour, values.uploadBytesPerHour)
	setInt(p, "PROTECTION_MAX_DELTA_BYTES", &pc.MaxDeltaBytes, values.maxDelta)
	setInt(p, "PROTECTION_MAX_SSE_PER_USER", &pc.MaxSSEPerUser, values.ssePerUser)
	setInt(p, "PROTECTION_MAX_SSE_TOTAL", &pc.MaxSSETotal, values.sseTotal)
	setInt(p, "PROTECTION_MAX_COLLAB_PER_USER", &pc.MaxCollabPerUser, values.collabUser)
	setFloat(p, "PROTECTION_COLLAB_MESSAGES_PER_SECOND", &pc.CollabMessagesPerSecond, values.collabRate)
	setInt(p, "PROTECTION_COLLAB_MESSAGES_BURST", &pc.CollabMessagesBurst, int(values.collabBurst))
	setInt(p, "PROTECTION_LOGIN_FAILURES_BEFORE_LOCKOUT", &pc.LoginFailuresBeforeLockout, values.loginFailures)
	setInt(p, "PROTECTION_MAX_CONCURRENT_PASSWORD_HASHES", &pc.MaxConcurrentPasswordHashes, values.hashes)
	setInt(p, "PROTECTION_RATE_LIMIT_MAX_KEYS", &pc.RateLimitMaxKeys, values.maxKeys)

	if pc.RateLimitMaxKeys < 1000 {
		return fmt.Errorf("CORTEX_PROTECTION_RATE_LIMIT_MAX_KEYS must be at least 1000")
	}
	if pc.MaxDeltaBytes <= 0 {
		return fmt.Errorf("CORTEX_PROTECTION_MAX_DELTA_BYTES must be positive")
	}
	if pc.MaxConcurrentPasswordHashes <= 0 {
		return fmt.Errorf("CORTEX_PROTECTION_MAX_CONCURRENT_PASSWORD_HASHES must be positive")
	}
	return nil
}

func applyFeatures(cfg *Config, p preset) error {
	enabled := map[string]bool{"collab": true, "avatars": true, "guests": true, "public_links": true}
	if p.explicit("FEATURES") {
		enabled = map[string]bool{}
		for _, raw := range cfg.FeaturesRaw {
			name := strings.ToLower(strings.TrimSpace(raw))
			if name == "" {
				continue
			}
			if !slices.Contains(knownFeatures, name) {
				return fmt.Errorf("CORTEX_FEATURES has unknown feature %q (known: %s)", name, strings.Join(knownFeatures, ", "))
			}
			enabled[name] = true
		}
	}
	legacy := []struct {
		env    string
		name   string
		target *bool
	}{
		{"FEATURE_COLLAB", "collab", &cfg.Feature.Collab},
		{"FEATURE_AVATARS", "avatars", &cfg.Feature.Avatars},
		{"FEATURE_GUESTS", "guests", &cfg.Feature.Guests},
		{"FEATURE_PUBLIC_LINKS", "public_links", &cfg.Feature.PublicLinks},
		{"METRICS_ENABLED", "metrics", &cfg.Metrics.Enabled},
	}
	var deprecated []string
	for _, item := range legacy {
		if p.explicit(item.env) {
			deprecated = append(deprecated, "CORTEX_"+item.env)
			continue
		}
		*item.target = enabled[item.name]
	}
	if len(deprecated) > 0 {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s are deprecated; list enabled features in CORTEX_FEATURES", strings.Join(deprecated, ", ")))
	}
	return nil
}

func applyTenancy(cfg *Config, p preset) error {
	multi := cfg.Tenancy.Mode == "multi"
	quota := "0"
	if multi {
		quota = "1GiB"
	}
	if p.explicit("TENANCY_DEFAULT_VAULT_QUOTA") {
		quota = cfg.Tenancy.DefaultVaultQuotaRaw
	}
	size, err := ParseSize(quota)
	if err != nil {
		return fmt.Errorf("CORTEX_TENANCY_DEFAULT_VAULT_QUOTA: %w", err)
	}
	cfg.Tenancy.DefaultVaultQuota = size
	if !multi {
		return nil
	}
	setInt(p, "TENANCY_MAX_VAULTS_PER_USER", &cfg.Tenancy.MaxVaultsPerUser, 10)
	setInt(p, "TENANCY_MAX_DEVICES_PER_USER", &cfg.Tenancy.MaxDevicesPerUser, 10)
	setInt(p, "TENANCY_MAX_PENDING_INVITES_PER_VAULT", &cfg.Tenancy.MaxPendingInvites, 50)
	setInt(p, "TENANCY_MAX_SHARES_PER_VAULT", &cfg.Tenancy.MaxSharesPerVault, 200)
	return nil
}

func ParseSize(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	units := []struct {
		suffix string
		factor int64
	}{
		{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1},
	}
	for _, unit := range units {
		if number, ok := strings.CutSuffix(value, unit.suffix); ok {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
			if err != nil || parsed < 0 {
				return 0, fmt.Errorf("invalid size %q", raw)
			}
			return int64(parsed * float64(unit.factor)), nil
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	return parsed, nil
}

func (cfg *Config) Summary() map[string]any {
	features := []string{}
	for _, item := range []struct {
		name    string
		enabled bool
	}{
		{"collab", cfg.Feature.Collab}, {"avatars", cfg.Feature.Avatars}, {"guests", cfg.Feature.Guests},
		{"public_links", cfg.Feature.PublicLinks}, {"metrics", cfg.Metrics.Enabled},
		{"email_verification", cfg.Feature.EmailVerification}, {"password_reset", cfg.Feature.PasswordReset},
		{"subscription", cfg.Subscription.Enabled},
	} {
		if item.enabled {
			features = append(features, item.name)
		}
	}
	proxy := cfg.Proxy.Mode
	if cfg.Proxy.Mode == ProxyHops {
		proxy = fmt.Sprintf("hops:%d", cfg.Proxy.Hops)
	}
	return map[string]any{
		"environment":      cfg.Server.Environment,
		"deployment":       cfg.Deployment,
		"bus":              cfg.Bus,
		"migrate_on_start": cfg.Database.MigrateOnStart,
		"db_pool":          fmt.Sprintf("%d/%d", cfg.Database.MinConns, cfg.Database.MaxConns),
		"proxy":            proxy,
		"protection":       cfg.Protection.Level,
		"tenancy":          cfg.Tenancy.Mode,
		"vault_quota":      cfg.Tenancy.DefaultVaultQuota,
		"mail":             cfg.Mail.Driver,
		"storage":          cfg.S3.Backend,
		"registration":     cfg.Auth.RegistrationMode,
		"features":         strings.Join(features, ","),
		"max_file_size":    cfg.Sync.MaxFileSize,
		"max_json_body":    cfg.Server.MaxJSONBodyBytes,
	}
}
