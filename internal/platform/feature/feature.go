package feature

import (
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
)

type Set struct {
	TenancyMode       string
	VaultCreation     string
	RegistrationMode  string
	Mail              bool
	EmailVerification bool
	PasswordReset     bool
	Collab            bool
	Avatars           bool
	Guests            bool
	PublicLinks       bool
	Subscription      bool
	Metrics           bool
}

type Limits struct {
	MaxFileSize             int64  `json:"max_file_size"`
	MaxUploadBytes          int64  `json:"max_upload_bytes"`
	MaxJSONBodyBytes        int64  `json:"max_json_body_bytes"`
	MaxDeltaBytes           int64  `json:"max_delta_bytes"`
	AvatarMaxBytes          int64  `json:"avatar_max_bytes"`
	BatchMaxItems           int    `json:"batch_max_items"`
	BatchMaxBytes           int64  `json:"batch_max_bytes"`
	MaxConcurrentUploads    int    `json:"max_concurrent_uploads_per_user,omitempty"`
	UploadBytesPerHour      int64  `json:"upload_bytes_per_hour,omitempty"`
	MaxEventStreamsPerUser  int    `json:"max_event_streams_per_user,omitempty"`
	MaxCollabSocketsPerUser int    `json:"max_collab_sockets_per_user,omitempty"`
	VaultQuotaBytes         int64  `json:"default_vault_quota_bytes,omitempty"`
	MaxVaultsPerUser        int    `json:"max_vaults_per_user,omitempty"`
	MaxDevicesPerUser       int    `json:"max_devices_per_user,omitempty"`
	Protection              string `json:"protection"`
}

func FromConfig(cfg *config.Config) Set {
	return Set{
		TenancyMode:       cfg.Tenancy.Mode,
		VaultCreation:     cfg.Tenancy.VaultCreation,
		RegistrationMode:  cfg.Auth.RegistrationMode,
		Mail:              cfg.MailEnabled(),
		EmailVerification: cfg.Feature.EmailVerification,
		PasswordReset:     cfg.Feature.PasswordReset,
		Collab:            cfg.Feature.Collab,
		Avatars:           cfg.Feature.Avatars,
		Guests:            cfg.Feature.Guests,
		PublicLinks:       cfg.Feature.PublicLinks,
		Subscription:      cfg.Subscription.Enabled,
		Metrics:           cfg.Metrics.Enabled,
	}
}

func (s Set) MultiTenant() bool {
	return s.TenancyMode == "multi"
}

func Require(enabled bool) func(http.Handler) http.Handler {
	if enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(http.Handler) http.Handler { return http.HandlerFunc(Disabled) }
}

func Disabled(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteErrorWithCode(w, http.StatusNotFound, "feature disabled on this server", "feature_disabled")
}

type capabilities struct {
	Version     string         `json:"version"`
	TenancyMode string         `json:"tenancy_mode"`
	Features    map[string]any `json:"features"`
	Limits      Limits         `json:"limits"`
}

func CapabilitiesHandler(set Set, limits Limits, version string) http.HandlerFunc {
	body := capabilities{
		Version:     version,
		TenancyMode: set.TenancyMode,
		Features: map[string]any{
			"collab":             set.Collab,
			"avatars":            set.Avatars,
			"guests":             set.Guests,
			"public_links":       set.PublicLinks,
			"email":              set.Mail,
			"email_invites":      set.Mail,
			"email_verification": set.EmailVerification,
			"password_reset":     set.PasswordReset,
			"subscription":       set.Subscription,
			"registration_mode":  set.RegistrationMode,
			"vault_creation":     set.VaultCreation,
		},
		Limits: limits,
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		httpx.WriteJSON(w, http.StatusOK, body)
	}
}
