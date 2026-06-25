package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/abacatepay"
	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/collab"
	devopsadapter "github.com/cortexnotes/cortex-sync/internal/adapter/devops"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/adapter/handler"
	"github.com/cortexnotes/cortex-sync/internal/adapter/middleware"
	pgadapter "github.com/cortexnotes/cortex-sync/internal/adapter/postgres"
	s3adapter "github.com/cortexnotes/cortex-sync/internal/adapter/s3"
	"github.com/cortexnotes/cortex-sync/internal/adapter/sse"
	"github.com/cortexnotes/cortex-sync/internal/config"
	"github.com/cortexnotes/cortex-sync/internal/job"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	if cfg.Server.Environment == "production" {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := pgadapter.RunMigrationCommand(cfg.Database, os.Args[2:]); err != nil {
			log.Fatal().Err(err).Msg("migration failed")
		}
		return
	}

	hasher := auth.NewBcryptHasher()
	tokenGen := auth.NewJWTGenerator(cfg.Auth.AccessTokenSecret, cfg.Auth.AccessTokenExpiry, cfg.Auth.Issuer)

	var (
		userRepo         port.UserRepository
		deviceRepo       port.DeviceRepository
		refreshTokenRepo port.RefreshTokenRepository
		vaultRepo        port.VaultRepository
		memberRepo       port.VaultMemberRepository
		inviteRepo       port.VaultInviteRepository
		keyRepo          port.VaultKeyRepository
		encryptionRepo   port.VaultEncryptionRepository
		snapshotRepo     port.FileSnapshotRepository
		deltaRepo        port.FileDeltaRepository
		latestRepo       port.FileLatestRepository
		eventRepo        port.SyncEventRepository
		collabRepo       port.CollabDocumentRepository
		subscriptionRepo port.SubscriptionRepository
		blobStorage      port.BlobStorage
		tx               port.Transactor
		dbPool           *pgxpool.Pool
	)

	ctx := context.Background()

	if cfg.UseFakeRepos {
		log.Info().Msg("using in-memory fake repositories")
		userRepo = fake.NewUserRepository()
		deviceRepo = fake.NewDeviceRepository()
		refreshTokenRepo = fake.NewRefreshTokenRepository()
		vaultRepo = fake.NewVaultRepository()
		memberRepo = fake.NewVaultMemberRepository()
		inviteRepo = fake.NewVaultInviteRepository()
		keyRepo = fake.NewVaultKeyRepository()
		encryptionRepo = fake.NewVaultEncryptionRepository()
		snapshotRepo = fake.NewFileSnapshotRepository()
		deltaRepo = fake.NewFileDeltaRepository()
		latestRepo = fake.NewFileLatestRepository()
		eventRepo = fake.NewSyncEventRepository()
		collabRepo = fake.NewCollabDocumentRepository()
		subscriptionRepo = fake.NewSubscriptionRepository()
		blobStorage = fake.NewBlobStorage()
		tx = fake.NewTransactor()
	} else {
		log.Info().Msg("connecting to PostgreSQL")
		dbPool, err = pgadapter.NewPool(ctx, cfg.Database)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to connect to database")
		}
		defer dbPool.Close()

		userRepo = pgadapter.NewUserRepository(dbPool)
		deviceRepo = pgadapter.NewDeviceRepository(dbPool)
		refreshTokenRepo = pgadapter.NewRefreshTokenRepository(dbPool)
		vaultRepo = pgadapter.NewVaultRepository(dbPool)
		memberRepo = pgadapter.NewVaultMemberRepository(dbPool)
		inviteRepo = pgadapter.NewVaultInviteRepository(dbPool)
		keyRepo = pgadapter.NewVaultKeyRepository(dbPool)
		encryptionRepo = pgadapter.NewVaultEncryptionRepository(dbPool)
		snapshotRepo = pgadapter.NewFileSnapshotRepository(dbPool)
		deltaRepo = pgadapter.NewFileDeltaRepository(dbPool)
		latestRepo = pgadapter.NewFileLatestRepository(dbPool)
		eventRepo = pgadapter.NewSyncEventRepository(dbPool)
		collabRepo = pgadapter.NewCollabDocumentRepository(dbPool)
		subscriptionRepo = pgadapter.NewSubscriptionRepository(dbPool)

		log.Info().Msg("connecting to S3/MinIO")
		s3Storage, s3Err := s3adapter.NewBlobStorage(ctx, cfg.S3)
		if s3Err != nil {
			log.Fatal().Err(s3Err).Msg("failed to connect to S3")
		}
		blobStorage = s3Storage
		tx = pgadapter.NewTransactor(dbPool)
	}

	devOpsNotifier := port.DevOpsNotifier(devopsadapter.NewNoopNotifier())
	if cfg.DevOps.DiscordWebhookURL != "" {
		devOpsCtx, devOpsCancel := context.WithCancel(ctx)
		defer devOpsCancel()
		discordNotifier := devopsadapter.NewDiscordNotifier(
			cfg.DevOps.DiscordWebhookURL,
			cfg.DevOps.DiscordUsername,
			cfg.DevOps.DiscordTimeout,
			cfg.Server.Environment,
		)
		devOpsNotifier = devopsadapter.NewAsyncNotifier(devOpsCtx, discordNotifier, 64)
		log.Info().Msg("discord devops notifications enabled")
	} else {
		log.Info().Msg("discord devops notifications disabled")
	}

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, cfg.Auth.RefreshTokenExpiry)
	authUC.SetRegistrationMode(cfg.Auth.RegistrationMode)
	authUC.SetDevOpsNotifier(devOpsNotifier)
	deviceUC := usecase.NewDeviceUsecase(deviceRepo, refreshTokenRepo)
	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, tx)
	memberUC := usecase.NewVaultMemberUsecase(memberRepo, keyRepo, userRepo)
	inviteUC := usecase.NewVaultInviteUsecase(inviteRepo, memberRepo, keyRepo, userRepo, vaultRepo, tx)
	encryptionUC := usecase.NewVaultEncryptionUsecase(encryptionRepo, memberRepo)
	fileUC := usecase.NewFileUsecase(snapshotRepo, deltaRepo, latestRepo, eventRepo, memberRepo, userRepo, deviceRepo, blobStorage, tx)
	fileUC.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: cfg.Sync.MaxDeltasBeforeSnapshot,
		MaxDeltaSizeRatio:       cfg.Sync.MaxDeltaSizeRatio,
	})
	fileUC.SetMaxFileSize(cfg.Sync.MaxFileSize)
	fileUC.SetMaxSnapshotsPerFile(cfg.Sync.MaxSnapshotsPerFile)

	broker := sse.NewBroker(64)
	fileUC.SetBroker(broker)

	collabBroker := collab.NewBroker(cfg.Collab.MaxPeersPerRoom, cfg.Collab.MaxBufBytes)

	if !cfg.UseFakeRepos && dbPool != nil {
		listener := pgadapter.NewListener(dbPool, broker)
		listenerCtx, listenerCancel := context.WithCancel(ctx)
		defer listenerCancel()
		go listener.Run(listenerCtx)
		log.Info().Msg("pg notify listener started")
	}

	authHandler := handler.NewAuthHandler(authUC)
	deviceHandler := handler.NewDeviceHandler(deviceUC)
	vaultHandler := handler.NewVaultHandler(vaultUC)
	memberHandler := handler.NewVaultMemberHandler(memberUC)
	inviteHandler := handler.NewVaultInviteHandler(inviteUC)
	fileHandler := handler.NewFileHandler(fileUC)
	sseHandler := handler.NewSSEHandler(broker, eventRepo, memberRepo)
	encryptionHandler := handler.NewVaultEncryptionHandler(encryptionUC)
	collabHandler := handler.NewCollabHandler(collabBroker, collabRepo, memberRepo, tokenGen, broker, cfg.Collab.FlushInterval)

	var subscriptionHandler *handler.SubscriptionHandler
	var entitlementChecker *handler.EntitlementChecker
	if cfg.Subscription.Enabled {
		abacateClient := abacatepay.NewClient(cfg.Subscription.APIKey, cfg.Subscription.ProductID)
		abacateClient.SetBaseURL(cfg.Subscription.AbacatePayBaseURL)
		entitlementChecker = handler.NewEntitlementChecker(subscriptionRepo, cfg.Subscription.CacheTTL)
		subscriptionUC := usecase.NewSubscriptionUsecase(subscriptionRepo, abacateClient, userRepo, cfg.Subscription.ProductID, cfg.Subscription.RenewalGrace)
		subscriptionUC.SetDevOpsNotifier(devOpsNotifier)
		subscriptionHandler = handler.NewSubscriptionHandler(
			subscriptionUC,
			handler.WebhookSecurity{
				Secret:  cfg.Subscription.WebhookSecret,
				HMACKey: cfg.Subscription.WebhookHMACKey,
			},
			entitlementChecker,
		)
		log.Info().Msg("subscription validation enabled")
	} else {
		log.Info().Msg("subscription validation disabled (self-hosted mode)")
	}

	if cfg.Subscription.Enabled && !cfg.UseFakeRepos && dbPool != nil && entitlementChecker != nil {
		subscriptionListener := pgadapter.NewSubscriptionListener(dbPool, entitlementChecker.Invalidate)
		subscriptionListenerCtx, subscriptionListenerCancel := context.WithCancel(ctx)
		defer subscriptionListenerCancel()
		go subscriptionListener.Run(subscriptionListenerCtx)
		log.Info().Msg("subscription pg notify listener started")
	}

	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Device-ID", "X-Device-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: cfg.CORS.AllowCredentials,
		MaxAge:           300,
	}))
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Metrics)
	r.Use(middleware.BodyLimit(cfg.Server.MaxJSONBodyBytes, cfg.Server.MaxUploadBytes))
	r.Use(middleware.NewRateLimiter(
		rate.Limit(cfg.RateLimit.RequestsPerSecond),
		cfg.RateLimit.Burst,
		middleware.WithTrustedProxyHeaders(cfg.Server.TrustProxyHeaders),
	).Middleware)

	r.Get("/health", handler.HealthCheck)
	r.Get("/ready", handler.NewReadinessHandler(dbPool, blobStorage).Check)

	if cfg.Metrics.Enabled {
		r.Handle(cfg.Metrics.Path, promhttp.Handler())
	}

	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Post("/token/refresh", authHandler.Refresh)
	})

	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Post("/auth/v1/logout", authHandler.Logout)

		r.Route("/devices/v1", func(r chi.Router) {
			r.Get("/", deviceHandler.List)
			r.Get("/{deviceID}", deviceHandler.Get)
			r.Delete("/{deviceID}", deviceHandler.Revoke)
			r.Patch("/{deviceID}", deviceHandler.Update)
			r.Put("/{deviceID}/sync-cursor", deviceHandler.UpdateSyncCursor)
		})

		if cfg.Subscription.Enabled {
			r.Route("/subscription/v1", func(r chi.Router) {
				r.Post("/checkout", subscriptionHandler.CreateCheckout)
				r.Get("/status", subscriptionHandler.GetStatus)
			})
		}

		requireEntitlement := func(next http.Handler) http.Handler {
			return next
		}
		if entitlementChecker != nil {
			requireEntitlement = entitlementChecker.Middleware
		}

		r.Route("/vaults/v1", func(r chi.Router) {
			r.Get("/", vaultHandler.List)
			r.With(requireEntitlement).Post("/", vaultHandler.Create)

			r.Get("/invites", inviteHandler.ListMyInvites)
			r.With(requireEntitlement).Post("/invites/accept", inviteHandler.Accept)

			r.Route("/{vaultID}", func(r chi.Router) {
				r.Get("/", vaultHandler.Get)
				r.With(requireEntitlement).Patch("/", vaultHandler.Update)
				r.With(requireEntitlement).Delete("/", vaultHandler.Delete)

				r.Route("/members", func(r chi.Router) {
					r.Get("/", memberHandler.List)
					r.With(requireEntitlement).Patch("/{userID}", memberHandler.UpdateRole)
					r.With(requireEntitlement).Delete("/{userID}", memberHandler.Remove)
				})

				r.Route("/invites", func(r chi.Router) {
					r.Get("/", inviteHandler.ListByVault)
					r.With(requireEntitlement).Post("/", inviteHandler.Create)
					r.With(requireEntitlement).Delete("/{inviteID}", inviteHandler.Delete)
				})
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(requireEntitlement)
			r.Route("/sync/v1/vaults/{vaultID}", func(r chi.Router) {
				r.Post("/files", fileHandler.UploadSnapshot)
				r.Get("/files", fileHandler.DownloadSnapshot)
				r.Delete("/files", fileHandler.DeleteFile)
				r.Post("/files/deltas", fileHandler.UploadDelta)
				r.Get("/files/deltas", fileHandler.DownloadDeltas)
				r.Post("/files/rename", fileHandler.RenameFile)
				r.Post("/files/restore", fileHandler.RestoreFile)
				r.Post("/files/bulk", fileHandler.BulkGetFileInfo)
				r.Get("/files/info", fileHandler.GetFileInfo)
				r.Get("/files/list", fileHandler.ListFiles)
				r.Get("/files/history", fileHandler.GetHistory)
				r.Get("/changes", fileHandler.ListChanges)
				r.Get("/events", sseHandler.Events)
				r.Post("/collab/tickets", collabHandler.CreateTicket)
				r.Get("/collab/peers", collabHandler.GetPeers)
				r.Get("/encryption", encryptionHandler.Get)
				r.Post("/encryption", encryptionHandler.Create)
			})
		})
	})

	r.Get("/sync/v1/vaults/{vaultID}/collab", collabHandler.Connect)

	if cfg.Subscription.Enabled {
		r.Post("/webhooks/abacatepay", subscriptionHandler.HandleWebhook)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	cleanerCtx, cleanerCancel := context.WithCancel(context.Background())
	cleaner := job.NewCleaner(refreshTokenRepo, inviteRepo, time.Hour)
	cleaner.SetSyncEvents(eventRepo, cfg.Sync.EventRetention)
	go cleaner.Run(cleanerCtx)

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().Str("addr", addr).Msg("starting server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	<-shutdown
	log.Info().Msg("shutting down server")

	cleanerCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("server forced to shutdown")
	}

	log.Info().Msg("server stopped")
}
