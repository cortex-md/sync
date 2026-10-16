package server

import (
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/account"
	"github.com/cortexnotes/cortex-sync/internal/auth"
	"github.com/cortexnotes/cortex-sync/internal/device"
	"github.com/cortexnotes/cortex-sync/internal/file"
	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/feature"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/middleware"
	"github.com/cortexnotes/cortex-sync/internal/platform/pages"
	"github.com/cortexnotes/cortex-sync/internal/realtime"
	"github.com/cortexnotes/cortex-sync/internal/share"
	"github.com/cortexnotes/cortex-sync/internal/subscription"
	"github.com/cortexnotes/cortex-sync/internal/template"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type routes struct {
	auth             *auth.Handler
	account          *account.Handler
	device           *device.Handler
	vault            *vault.Handler
	member           *vault.MemberHandler
	access           *vault.AccessHandler
	invite           *vault.InviteHandler
	key              *vault.KeyHandler
	encryption       *vault.EncryptionHandler
	file             *file.Handler
	share            *share.Handler
	template         *template.Handler
	sse              *realtime.SSEHandler
	collab           *realtime.CollabHandler
	readiness        *ReadinessHandler
	subscription     *subscription.Handler
	entitlement      func(http.Handler) http.Handler
	vaultEntitlement func(http.Handler) http.Handler
	tokens           httpx.TokenValidator
	overview         *OverviewHandler
	protection       protection
}

func newRouter(cfg *config.Config, features feature.Set, h routes) http.Handler {
	r := chi.NewRouter()
	guard := h.protection

	r.Use(guard.clientIP)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Device-ID", "X-Device-Token", "X-Request-ID", "X-File-Path", "X-File-Path-Encoding", "X-Base-Version", "X-Local-Hash", "X-Checksum", "X-Content-Type", "Last-Event-ID", "If-None-Match"},
		ExposedHeaders:   []string{"X-Request-ID", "X-File-Path", "X-File-Path-Encoded", "X-File-Version", "X-Checksum", "X-Snapshot-ID", "X-Latest-Event-ID", "X-Next-Cursor", "ETag", "X-Batch-Count", "X-Size-Bytes", "X-Created-By", "X-Device-ID", "X-Author-Device-ID", "X-Snapshot-Version", "X-Has-More"},
		AllowCredentials: cfg.CORS.AllowCredentials,
		MaxAge:           300,
	}))
	r.Use(middleware.RequestID)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(chimw.Compress(5, "application/json"))
	if features.Metrics {
		r.Use(middleware.Metrics)
	}
	r.Use(middleware.BodyLimit(cfg.Server.MaxJSONBodyBytes))
	r.Use(guard.global)

	sensitive := guard.sensitive
	public := guard.public

	collab := feature.Require(features.Collab && h.collab != nil)
	avatars := feature.Require(features.Avatars)
	verification := feature.Require(features.EmailVerification)
	passwordReset := feature.Require(features.PasswordReset)
	publicLinks := feature.Require(features.PublicLinks)
	passthrough := func(next http.Handler) http.Handler { return next }
	entitled, vaultEntitled := passthrough, passthrough
	if h.entitlement != nil {
		entitled = h.entitlement
	}
	if h.vaultEntitlement != nil {
		vaultEntitled = h.vaultEntitlement
	}

	r.Get("/health", HealthCheck)
	r.Get("/ready", h.readiness.Check)
	if features.Metrics {
		r.Handle(cfg.Metrics.Path, promhttp.Handler())
	}
	r.Get("/meta/v1/capabilities", feature.CapabilitiesHandler(features, feature.Limits{
		MaxFileSize:             cfg.Sync.MaxFileSize,
		MaxUploadBytes:          cfg.Server.MaxUploadBytes,
		MaxJSONBodyBytes:        cfg.Server.MaxJSONBodyBytes,
		MaxDeltaBytes:           cfg.Protection.MaxDeltaBytes,
		AvatarMaxBytes:          cfg.Avatar.MaxBytes,
		BatchMaxItems:           file.BatchMaxItems,
		BatchMaxBytes:           file.BatchMaxBytes,
		MaxConcurrentUploads:    cfg.Protection.MaxConcurrentUploadsPerUser,
		UploadBytesPerHour:      cfg.Protection.UploadBytesPerHour,
		MaxEventStreamsPerUser:  cfg.Protection.MaxSSEPerUser,
		MaxCollabSocketsPerUser: cfg.Protection.MaxCollabPerUser,
		VaultQuotaBytes:         cfg.Tenancy.DefaultVaultQuota,
		MaxVaultsPerUser:        cfg.Tenancy.MaxVaultsPerUser,
		MaxDevicesPerUser:       cfg.Tenancy.MaxDevicesPerUser,
		Protection:              cfg.Protection.Level,
	}, Version))

	r.Route("/auth/v1", func(r chi.Router) {
		r.With(sensitive).Post("/prelogin", h.auth.Prelogin)
		r.With(sensitive, guard.registrations).Post("/register", h.auth.Register)
		r.With(sensitive).Post("/login", h.auth.Login)
		r.Post("/token/refresh", h.auth.Refresh)
	})

	r.Route("/account/v1", func(r chi.Router) {
		r.With(verification).Get("/email/verify", h.account.VerifyEmailPage)
		r.With(verification, sensitive).Post("/email/verify", h.account.VerifyEmail)
		r.With(verification).Get("/email/confirm-change", h.account.ConfirmEmailChangePage)
		r.With(verification, sensitive).Post("/email/confirm-change", h.account.ConfirmEmailChange)
		r.With(passwordReset, sensitive).Post("/password/forgot", h.account.ForgotPassword)
		r.With(passwordReset).Get("/password/reset", h.account.ResetPasswordPage)
		r.With(passwordReset, sensitive).Post("/password/reset/prepare", h.account.PrepareResetPassword)
		r.With(passwordReset, sensitive).Post("/password/reset", h.account.ResetPassword)
	})

	r.With(avatars, public).Get("/avatars/v1/{userID}/{file}", h.account.ServeAvatar)
	r.Handle("/assets/v1/*", pages.Assets())
	r.With(public).Get("/invite/{token}", h.invite.InvitePage)
	r.With(sensitive).Post("/invite/{token}/decline", h.invite.DeclinePage)
	r.With(publicLinks, public).Get("/s/{shareID}", h.share.Page)
	r.With(publicLinks, public).Get("/share/v1/{shareID}", h.share.Content)
	r.With(publicLinks).Handle("/share-assets/*", share.Assets())
	r.With(publicLinks, public).Get("/template/v1/market", h.template.Market)
	r.With(publicLinks, public).Get("/template/v1/{slug}", h.template.Public)
	r.With(sensitive).Get("/invites/v1/{token}", h.invite.Preview)

	r.Group(func(r chi.Router) {
		r.Use(httpx.AuthMiddleware(h.tokens))
		r.Use(httpx.DeviceMiddleware)
		r.Use(guard.user)
		r.Post("/auth/v1/logout", h.auth.Logout)

		r.With(guard.heavy).Get("/account/v1/overview", h.overview.Account)

		r.Route("/account/v1/me", func(r chi.Router) {
			r.Get("/", h.account.Me)
			r.Patch("/", h.account.UpdateProfile)
			r.With(sensitive).Put("/password", h.account.ChangePassword)
			r.With(sensitive).Post("/email", h.account.RequestEmailChange)
			r.With(verification, sensitive).Post("/email/verification", h.account.SendEmailVerification)
			r.With(avatars, guard.avatarBody).Put("/avatar", h.account.SetAvatar)
			r.With(avatars).Delete("/avatar", h.account.DeleteAvatar)
			r.Put("/public-key", h.key.SetPublicKey)
			r.Get("/keys", h.account.GetKeys)
			r.With(sensitive).Put("/keys", h.account.SetupKeys)
			r.Put("/recovery", h.account.SetRecoveryKey)
		})
		r.Get("/users/v1/{userID}/public-key", h.key.GetPublicKey)

		r.Route("/devices/v1", func(r chi.Router) {
			r.Get("/", h.device.List)
			r.Get("/{deviceID}", h.device.Get)
			r.Delete("/{deviceID}", h.device.Revoke)
			r.Patch("/{deviceID}", h.device.Update)
		})

		if h.subscription != nil {
			r.Route("/subscription/v1", func(r chi.Router) {
				r.Post("/checkout", h.subscription.CreateCheckout)
				r.Get("/status", h.subscription.GetStatus)
			})
		}

		r.Route("/vaults/v1", func(r chi.Router) {
			r.Get("/", h.vault.List)
			r.With(entitled).Post("/", h.vault.Create)

			r.Get("/invites", h.invite.ListMyInvites)
			r.With(entitled).Post("/invites/accept", h.invite.Accept)

			r.Route("/{vaultID}", func(r chi.Router) {
				r.Get("/", h.vault.Get)
				r.With(guard.heavy).Get("/overview", h.overview.Vault)
				r.With(entitled).Patch("/", h.vault.Update)
				r.With(entitled).Delete("/", h.vault.Delete)

				r.Route("/members", func(r chi.Router) {
					r.Get("/", h.member.List)
					r.With(entitled).Patch("/{userID}", h.member.UpdateRole)
					r.With(entitled).Delete("/{userID}", h.member.Remove)
				})

				r.Route("/invites", func(r chi.Router) {
					r.Get("/", h.invite.ListByVault)
					r.With(entitled).Post("/", h.invite.Create)
					r.With(entitled).Delete("/{inviteID}", h.invite.Delete)
				})

				r.Route("/keys", func(r chi.Router) {
					r.Get("/me", h.key.GetMine)
					r.Put("/me", h.key.PutMine)
					r.Get("/pending", h.key.ListPending)
					r.Put("/{userID}", h.key.Grant)
				})

				r.Route("/groups", func(r chi.Router) {
					r.Get("/", h.access.ListGroups)
					r.With(entitled).Post("/", h.access.CreateGroup)
					r.With(entitled).Patch("/{groupID}", h.access.UpdateGroup)
					r.With(entitled).Delete("/{groupID}", h.access.DeleteGroup)
				})

				r.Route("/access", func(r chi.Router) {
					r.Get("/", h.access.ListRules)
					r.With(entitled).Put("/", h.access.PutRule)
				})
			})
		})

		r.Group(func(r chi.Router) {
			r.Route("/sync/v1/vaults/{vaultID}", func(r chi.Router) {
				r.Use(vaultEntitled)
				r.With(guard.upload).Post("/files", h.file.UploadSnapshot)
				r.Get("/files", h.file.DownloadSnapshot)
				r.Delete("/files", h.file.DeleteFile)
				r.With(guard.batch).Post("/files/batch", h.file.BatchDownload)
				r.Post("/files/deltas", h.file.UploadDelta)
				r.Get("/files/deltas", h.file.DownloadDeltas)
				r.Post("/files/rename", h.file.RenameFile)
				r.Post("/files/restore", h.file.RestoreFile)
				r.Post("/files/bulk", h.file.BulkGetFileInfo)
				r.Get("/files/info", h.file.GetFileInfo)
				r.With(guard.firstPage(guard.heavy)).Get("/files/list", h.file.ListFiles)
				r.Get("/files/history", h.file.GetHistory)
				r.Get("/changes", h.file.ListChanges)
				r.Put("/cursor", h.file.UpdateCursor)
				r.With(publicLinks).Route("/shares", func(r chi.Router) {
					r.Get("/", h.share.List)
					r.Post("/", h.share.Create)
					r.With(guard.heavy, guard.upload).Put("/{shareID}/content", h.share.Publish)
					r.Patch("/{shareID}", h.share.Update)
					r.Delete("/{shareID}", h.share.Revoke)
				})
				r.Route("/templates", func(r chi.Router) {
					r.Get("/", h.template.List)
					r.With(guard.heavy).Post("/", h.template.Create)
					r.Get("/{templateID}/payload", h.template.GetPayload)
					r.With(guard.heavy).Put("/{templateID}/payload", h.template.ReplacePayload)
					r.Patch("/{templateID}", h.template.Update)
					r.Delete("/{templateID}", h.template.Delete)
				})
				r.With(guard.streams).Get("/events", h.sse.Events)
				r.Get("/encryption", h.encryption.Get)
				r.Post("/encryption", h.encryption.Create)
				r.With(collab).Get("/presence", collabHandlerFunc(h.collab, (*realtime.CollabHandler).GetVaultPresence))
				r.With(collab).Post("/collab/tickets", collabHandlerFunc(h.collab, (*realtime.CollabHandler).CreateTicket))
				r.With(collab).Get("/collab/peers", collabHandlerFunc(h.collab, (*realtime.CollabHandler).GetPeers))
			})
		})
	})

	r.With(collab).Get("/sync/v1/vaults/{vaultID}/collab", collabHandlerFunc(h.collab, (*realtime.CollabHandler).Connect))

	if h.subscription != nil {
		r.Post("/webhooks/stripe", h.subscription.HandleWebhook)
	}

	return r
}

func collabHandlerFunc(h *realtime.CollabHandler, method func(*realtime.CollabHandler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	if h == nil {
		return feature.Disabled
	}
	return func(w http.ResponseWriter, r *http.Request) {
		method(h, w, r)
	}
}
