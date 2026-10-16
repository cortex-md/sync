package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func lookupFrom(values map[string]string) lookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func resolve(t *testing.T, cfg *Config, values map[string]string) error {
	t.Helper()
	if cfg.Deployment == "" {
		cfg.Deployment = values["CORTEX_DEPLOYMENT"]
	}
	if cfg.Tenancy.Mode == "" {
		cfg.Tenancy.Mode = "single"
	}
	return applyProfiles(cfg, lookupFrom(values))
}

func TestSingleDeploymentUsesMemoryBusAndSmallPool(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, resolve(t, cfg, nil))

	require.Equal(t, DeploymentSingle, cfg.Deployment)
	require.Equal(t, BusMemory, cfg.Bus)
	require.True(t, cfg.Database.MigrateOnStart)
	require.EqualValues(t, 5, cfg.Database.MaxConns)
	require.EqualValues(t, 0, cfg.Database.MinConns)
	require.Equal(t, 2*time.Minute, cfg.Database.MaxConnIdleTime)
}

func TestClusterDeploymentUsesPostgresBusAndWarnsOnMigrateOnStart(t *testing.T) {
	cfg := &Config{Deployment: "cluster", Database: DatabaseConfig{MigrateOnStart: true}}
	require.NoError(t, resolve(t, cfg, map[string]string{"CORTEX_DATABASE_MIGRATE_ON_START": "true"}))

	require.Equal(t, BusPostgres, cfg.Bus)
	require.True(t, cfg.Database.MigrateOnStart)
	require.EqualValues(t, 10, cfg.Database.MaxConns)
	require.Len(t, cfg.Warnings, 1)
}

func TestExplicitOverrideWinsOverPreset(t *testing.T) {
	cfg := &Config{Database: DatabaseConfig{MaxConns: 3}, Sync: SyncConfig{MaxFileSize: 1234}}
	require.NoError(t, resolve(t, cfg, map[string]string{
		"CORTEX_DATABASE_MAX_CONNS": "3",
		"CORTEX_SYNC_MAX_FILE_SIZE": "1234",
	}))

	require.EqualValues(t, 3, cfg.Database.MaxConns)
	require.EqualValues(t, 1234, cfg.Sync.MaxFileSize)
	require.EqualValues(t, 25*mib, cfg.Server.MaxUploadBytes)
}

func TestProtectionPresets(t *testing.T) {
	cases := map[string]func(t *testing.T, cfg *Config){
		"": func(t *testing.T, cfg *Config) {
			require.Equal(t, ProtectionStandard, cfg.Protection.Level)
			require.EqualValues(t, 20, cfg.RateLimit.RequestsPerSecond)
			require.Equal(t, 4, cfg.Protection.MaxConcurrentUploads)
			require.EqualValues(t, 2*mib, cfg.Server.MaxJSONBodyBytes)
		},
		"off": func(t *testing.T, cfg *Config) {
			require.False(t, cfg.Protection.Enabled())
			require.Zero(t, cfg.RateLimit.RequestsPerSecond)
			require.Zero(t, cfg.Protection.MaxConcurrentUploads)
			require.EqualValues(t, 100*mib, cfg.Sync.MaxFileSize)
		},
		"strict": func(t *testing.T, cfg *Config) {
			require.Equal(t, 2, cfg.Protection.MaxConcurrentUploads)
			require.EqualValues(t, 10*mib, cfg.Sync.MaxFileSize)
		},
	}
	for level, check := range cases {
		t.Run(level, func(t *testing.T) {
			values := map[string]string{}
			if level != "" {
				values["CORTEX_PROTECTION"] = level
			}
			cfg := &Config{}
			require.NoError(t, resolve(t, cfg, values))
			check(t, cfg)
		})
	}
}

func TestProxyModes(t *testing.T) {
	cases := map[string]ProxyConfig{
		"":           {Mode: ProxyNone},
		"none":       {Mode: ProxyNone},
		"render":     {Mode: ProxyHops, Hops: 1},
		"hops:2":     {Mode: ProxyHops, Hops: 2},
		"cloudflare": {Mode: ProxyCloudflare},
	}
	for raw, expected := range cases {
		cfg := &Config{ProxyRaw: raw}
		require.NoError(t, resolve(t, cfg, nil), raw)
		require.Equal(t, expected, cfg.Proxy, raw)
		require.Equal(t, expected.Mode != ProxyNone, cfg.Server.TrustProxyHeaders, raw)
	}

	legacy := &Config{Server: ServerConfig{TrustProxyHeaders: true}}
	require.NoError(t, resolve(t, legacy, nil))
	require.Equal(t, ProxyConfig{Mode: ProxyHops, Hops: 1}, legacy.Proxy)
	require.NotEmpty(t, legacy.Warnings)

	for _, invalid := range []string{"hops:0", "hops:x", "nginx"} {
		require.Error(t, resolve(t, &Config{ProxyRaw: invalid}, nil), invalid)
	}
}

func TestFeaturesListAndLegacyOverrides(t *testing.T) {
	defaults := &Config{}
	require.NoError(t, resolve(t, defaults, nil))
	require.True(t, defaults.Feature.Collab)
	require.True(t, defaults.Feature.PublicLinks)
	require.False(t, defaults.Metrics.Enabled)

	listed := &Config{FeaturesRaw: []string{"avatars", "metrics"}}
	require.NoError(t, resolve(t, listed, map[string]string{"CORTEX_FEATURES": "avatars,metrics"}))
	require.False(t, listed.Feature.Collab)
	require.True(t, listed.Feature.Avatars)
	require.True(t, listed.Metrics.Enabled)

	legacy := &Config{Feature: FeatureConfig{Collab: false}}
	require.NoError(t, resolve(t, legacy, map[string]string{"CORTEX_FEATURE_COLLAB": "false"}))
	require.False(t, legacy.Feature.Collab)
	require.True(t, legacy.Feature.Guests)
	require.NotEmpty(t, legacy.Warnings)

	require.Error(t, resolve(t, &Config{FeaturesRaw: []string{"telepathy"}}, map[string]string{"CORTEX_FEATURES": "telepathy"}))
}

func TestTenancyLimitsOnlyInMultiMode(t *testing.T) {
	single := &Config{}
	require.NoError(t, resolve(t, single, nil))
	require.Zero(t, single.Tenancy.DefaultVaultQuota)
	require.Zero(t, single.Tenancy.MaxVaultsPerUser)

	multi := &Config{Tenancy: TenancyConfig{Mode: "multi", DefaultVaultQuotaRaw: "512MiB"}}
	require.NoError(t, resolve(t, multi, map[string]string{"CORTEX_TENANCY_DEFAULT_VAULT_QUOTA": "512MiB"}))
	require.EqualValues(t, 512*mib, multi.Tenancy.DefaultVaultQuota)
	require.Equal(t, 10, multi.Tenancy.MaxVaultsPerUser)
}

func TestRenderPortFallback(t *testing.T) {
	cfg := &Config{Server: ServerConfig{Port: 8080}}
	require.NoError(t, resolve(t, cfg, map[string]string{"PORT": "10000"}))
	require.Equal(t, 10000, cfg.Server.Port)

	explicit := &Config{Server: ServerConfig{Port: 9000}}
	require.NoError(t, resolve(t, explicit, map[string]string{"PORT": "10000", "CORTEX_SERVER_PORT": "9000"}))
	require.Equal(t, 9000, explicit.Server.Port)
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"": 0, "1024": 1024, "1GiB": 1 << 30, "1.5MiB": 3 << 19, "2MB": 2e6}
	for raw, expected := range cases {
		size, err := ParseSize(raw)
		require.NoError(t, err, raw)
		require.Equal(t, expected, size, raw)
	}
	_, err := ParseSize("lots")
	require.Error(t, err)
}
