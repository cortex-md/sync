package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func clearStorageEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"CORTEX_STORAGE_BACKEND",
		"CORTEX_STORAGE_MODE",
		"CORTEX_STORAGE_REMOTE_PROVIDER",
		"CORTEX_STORAGE_ENDPOINT",
		"CORTEX_STORAGE_ACCESS_KEY",
		"CORTEX_STORAGE_SECRET_KEY",
		"CORTEX_STORAGE_BUCKET",
		"CORTEX_STORAGE_REGION",
		"CORTEX_S3_PROVIDER",
		"CORTEX_S3_ENDPOINT",
		"CORTEX_S3_ACCESS_KEY",
		"CORTEX_S3_SECRET_KEY",
		"CORTEX_S3_BUCKET",
		"CORTEX_S3_USE_SSL",
		"CORTEX_S3_REGION",
		"CORTEX_S3_CREATE_BUCKET",
		"CORTEX_SERVER_ENV",
		"CORTEX_AUTH_ACCESS_TOKEN_SECRET",
		"CORTEX_AUTH_REGISTRATION_MODE",
		"CORTEX_USE_FAKE_REPOS",
		"CORTEX_CORS_ALLOWED_ORIGINS",
		"CORTEX_CORS_ALLOW_CREDENTIALS",
		"CORTEX_DATABASE_URL",
		"CORTEX_SUBSCRIPTION_ENABLED",
		"CORTEX_SUBSCRIPTION_API_KEY",
		"CORTEX_SUBSCRIPTION_PRODUCT_ID",
		"CORTEX_SUBSCRIPTION_WEBHOOK_SECRET",
		"CORTEX_SUBSCRIPTION_WEBHOOK_HMAC_KEY",
		"CORTEX_SUBSCRIPTION_CACHE_TTL",
		"CORTEX_SUBSCRIPTION_RENEWAL_GRACE",
		"CORTEX_SUBSCRIPTION_ABACATEPAY_BASE_URL",
		"CORTEX_DEVOPS_DISCORD_WEBHOOK_URL",
		"CORTEX_DEVOPS_DISCORD_USERNAME",
		"CORTEX_DEVOPS_DISCORD_TIMEOUT",
	}
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		require.NoError(t, os.Unsetenv(key))
		t.Cleanup(func() {
			if ok {
				require.NoError(t, os.Setenv(key, value))
				return
			}
			require.NoError(t, os.Unsetenv(key))
		})
	}
}

func TestLoadUsesLocalStorageByDefault(t *testing.T) {
	clearStorageEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "local", cfg.S3.Backend)
	require.Equal(t, "localhost:9000", cfg.S3.Endpoint)
	require.Equal(t, "minioadmin", cfg.S3.AccessKey)
	require.Equal(t, "minioadmin", cfg.S3.SecretKey)
	require.Equal(t, "cortex-snapshots", cfg.S3.Bucket)
	require.Equal(t, "us-east-1", cfg.S3.Region)
	require.False(t, cfg.S3.UseSSL)
	require.True(t, cfg.S3.CreateBucket)
	require.Equal(t, "open", cfg.Auth.RegistrationMode)
	require.Empty(t, cfg.DevOps.DiscordWebhookURL)
	require.Equal(t, "Cortex DevOps", cfg.DevOps.DiscordUsername)
	require.Equal(t, 5*time.Second, cfg.DevOps.DiscordTimeout)
}

func TestLoadUsesLocalStorageEndpointOverride(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_STORAGE_BACKEND", "local")
	t.Setenv("CORTEX_STORAGE_ENDPOINT", "http://minio:9000")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "local", cfg.S3.Backend)
	require.Equal(t, "minio:9000", cfg.S3.Endpoint)
	require.False(t, cfg.S3.UseSSL)
	require.True(t, cfg.S3.CreateBucket)
}

func TestLoadUsesRemoteR2Storage(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_STORAGE_BACKEND", "r2")
	t.Setenv("CORTEX_STORAGE_ENDPOINT", "https://account-id.r2.cloudflarestorage.com")
	t.Setenv("CORTEX_STORAGE_ACCESS_KEY", "access")
	t.Setenv("CORTEX_STORAGE_SECRET_KEY", "secret")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "r2", cfg.S3.Backend)
	require.Equal(t, "account-id.r2.cloudflarestorage.com", cfg.S3.Endpoint)
	require.Equal(t, "auto", cfg.S3.Region)
	require.True(t, cfg.S3.UseSSL)
	require.False(t, cfg.S3.CreateBucket)
}

func TestLoadUsesRemoteS3Storage(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_STORAGE_BACKEND", "s3")
	t.Setenv("CORTEX_STORAGE_ENDPOINT", "https://s3.example.com")
	t.Setenv("CORTEX_STORAGE_ACCESS_KEY", "access")
	t.Setenv("CORTEX_STORAGE_SECRET_KEY", "secret")
	t.Setenv("CORTEX_STORAGE_REGION", "sa-east-1")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "s3", cfg.S3.Backend)
	require.Equal(t, "s3.example.com", cfg.S3.Endpoint)
	require.Equal(t, "sa-east-1", cfg.S3.Region)
	require.True(t, cfg.S3.UseSSL)
	require.False(t, cfg.S3.CreateBucket)
}

func TestLoadUsesStorageSettingsFromConfigFile(t *testing.T) {
	clearStorageEnv(t)
	tempDir := t.TempDir()
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	configFile := []byte(
		"storage:\n" +
			"  backend: s3\n" +
			"  endpoint: https://s3.example.com\n" +
			"  access_key: access\n" +
			"  secret_key: secret\n",
	)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "config.yaml"), configFile, 0o644))
	require.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(currentDir))
	})

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "s3", cfg.S3.Backend)
	require.Equal(t, "s3.example.com", cfg.S3.Endpoint)
}

func TestLoadRejectsUnknownStorageBackend(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_STORAGE_BACKEND", "archive")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported storage backend")
}

func TestLoadRejectsLegacyLocalStackProvider(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_S3_PROVIDER", "localstack")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "localstack is no longer supported")
}

func TestLoadUsesLegacyR2Provider(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_S3_PROVIDER", "r2")
	t.Setenv("CORTEX_S3_ENDPOINT", "https://account-id.r2.cloudflarestorage.com")
	t.Setenv("CORTEX_S3_ACCESS_KEY", "access")
	t.Setenv("CORTEX_S3_SECRET_KEY", "secret")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "r2", cfg.S3.Backend)
	require.False(t, cfg.S3.CreateBucket)
}

func TestLoadUsesLegacyStorageModeAndRemoteProvider(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_STORAGE_MODE", "remote")
	t.Setenv("CORTEX_STORAGE_REMOTE_PROVIDER", "s3")
	t.Setenv("CORTEX_STORAGE_ENDPOINT", "https://s3.example.com")
	t.Setenv("CORTEX_STORAGE_ACCESS_KEY", "access")
	t.Setenv("CORTEX_STORAGE_SECRET_KEY", "secret")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "s3", cfg.S3.Backend)
}

func TestLoadRejectsRemoteWithoutEndpoint(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_STORAGE_BACKEND", "r2")
	t.Setenv("CORTEX_STORAGE_ACCESS_KEY", "access")
	t.Setenv("CORTEX_STORAGE_SECRET_KEY", "secret")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "storage endpoint is required")
}

func TestLoadAppliesProductionPolicyValidation(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_SERVER_ENV", "production")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "production auth access token secret")
}

func TestLoadAcceptsValidProductionPolicy(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_SERVER_ENV", "production")
	t.Setenv("CORTEX_AUTH_ACCESS_TOKEN_SECRET", "replace-with-at-least-32-random-characters")
	t.Setenv("CORTEX_AUTH_REGISTRATION_MODE", "first-user")
	t.Setenv("CORTEX_DATABASE_URL", "postgres://cortex:secret@postgres:5432/cortex_sync?sslmode=disable")

	cfg, err := Load()

	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "production", cfg.Server.Environment)
	require.Equal(t, "first-user", cfg.Auth.RegistrationMode)
}

func TestLoadAcceptsProductionDiscordDevOpsWebhook(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_SERVER_ENV", "production")
	t.Setenv("CORTEX_AUTH_ACCESS_TOKEN_SECRET", "replace-with-at-least-32-random-characters")
	t.Setenv("CORTEX_AUTH_REGISTRATION_MODE", "first-user")
	t.Setenv("CORTEX_DATABASE_URL", "postgres://cortex:secret@postgres:5432/cortex_sync?sslmode=disable")
	t.Setenv("CORTEX_DEVOPS_DISCORD_WEBHOOK_URL", "https://discord.com/api/webhooks/123/token")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "https://discord.com/api/webhooks/123/token", cfg.DevOps.DiscordWebhookURL)
}

func TestLoadRejectsProductionDiscordDevOpsWebhookOutsideDiscord(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_SERVER_ENV", "production")
	t.Setenv("CORTEX_AUTH_ACCESS_TOKEN_SECRET", "replace-with-at-least-32-random-characters")
	t.Setenv("CORTEX_AUTH_REGISTRATION_MODE", "first-user")
	t.Setenv("CORTEX_DATABASE_URL", "postgres://cortex:secret@postgres:5432/cortex_sync?sslmode=disable")
	t.Setenv("CORTEX_DEVOPS_DISCORD_WEBHOOK_URL", "https://example.com/webhook")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "production discord webhook url")
}

func TestLoadRejectsProductionSubscriptionWithoutSecrets(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_SERVER_ENV", "production")
	t.Setenv("CORTEX_AUTH_ACCESS_TOKEN_SECRET", "replace-with-at-least-32-random-characters")
	t.Setenv("CORTEX_AUTH_REGISTRATION_MODE", "first-user")
	t.Setenv("CORTEX_DATABASE_URL", "postgres://cortex:secret@postgres:5432/cortex_sync?sslmode=disable")
	t.Setenv("CORTEX_SUBSCRIPTION_ENABLED", "true")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "subscription api key")
}

func TestLoadAcceptsProductionSubscriptionSecrets(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_SERVER_ENV", "production")
	t.Setenv("CORTEX_AUTH_ACCESS_TOKEN_SECRET", "replace-with-at-least-32-random-characters")
	t.Setenv("CORTEX_AUTH_REGISTRATION_MODE", "first-user")
	t.Setenv("CORTEX_DATABASE_URL", "postgres://cortex:secret@postgres:5432/cortex_sync?sslmode=disable")
	t.Setenv("CORTEX_SUBSCRIPTION_ENABLED", "true")
	t.Setenv("CORTEX_SUBSCRIPTION_API_KEY", "abacate-key")
	t.Setenv("CORTEX_SUBSCRIPTION_PRODUCT_ID", "prod_123")
	t.Setenv("CORTEX_SUBSCRIPTION_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("CORTEX_SUBSCRIPTION_WEBHOOK_HMAC_KEY", "webhook-hmac")

	cfg, err := Load()

	require.NoError(t, err)
	require.True(t, cfg.Subscription.Enabled)
	require.Equal(t, "https://api.abacatepay.com/v2", cfg.Subscription.AbacatePayBaseURL)
	require.Equal(t, time.Minute, cfg.Subscription.CacheTTL)
}

func TestLoadRejectsUnknownRegistrationMode(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("CORTEX_AUTH_REGISTRATION_MODE", "invite-only")

	cfg, err := Load()

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth registration mode")
}
