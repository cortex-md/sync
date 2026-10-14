package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/account"
	"github.com/cortexnotes/cortex-sync/internal/auth"
	"github.com/cortexnotes/cortex-sync/internal/device"
	"github.com/cortexnotes/cortex-sync/internal/devops"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/file"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/avatar"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/feature"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/lifecycle"
	"github.com/cortexnotes/cortex-sync/internal/platform/mail"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres"
	"github.com/cortexnotes/cortex-sync/internal/platform/s3"
	"github.com/cortexnotes/cortex-sync/internal/realtime"
	"github.com/cortexnotes/cortex-sync/internal/share"
	"github.com/cortexnotes/cortex-sync/internal/subscription"
	"github.com/cortexnotes/cortex-sync/internal/template"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

var Version = "dev"

type Infra struct {
	Pool   *pgxpool.Pool
	Repos  Repositories
	Bus    bus.Bus
	PGBus  *postgres.SignalBus
	Mailer Mailer
}

type Server struct {
	cfg       *config.Config
	features  feature.Set
	infra     Infra
	lifecycle *lifecycle.Lifecycle
	handler   http.Handler
	events    *realtime.EventBroker
	listener  *postgres.Listener
	shares    *share.Service
	templates *template.Service
}

func New(ctx context.Context, cfg *config.Config, infra Infra) *Server {
	features := feature.FromConfig(cfg)
	repos := infra.Repos
	life := lifecycle.New()

	var outbox EmailOutboxRepository
	if features.Mail {
		outbox = repos.Outbox
	}
	var devOps devops.Notifier = devops.NewNoopNotifier()
	if cfg.DevOps.DiscordWebhookURL != "" {
		devOps = devops.NewAsyncNotifier(ctx, devops.NewDiscordNotifier(
			cfg.DevOps.DiscordWebhookURL, cfg.DevOps.DiscordUsername, cfg.DevOps.DiscordTimeout, cfg.Server.Environment,
		), 64)
	}

	hasher := auth.NewBcryptHasher()
	tokens := auth.NewJWTGenerator(cfg.Auth.AccessTokenSecret, cfg.Auth.AccessTokenExpiry, cfg.Auth.Issuer)
	authorizer := authz.NewCached(repos.Members)
	infra.Bus.Subscribe(bus.KindAccessChanged, func(signal bus.Signal) {
		authorizer.Invalidate(signal.VaultID, signal.UserID)
	})
	infra.Bus.Subscribe(bus.KindVaultAccessChanged, func(signal bus.Signal) {
		authorizer.InvalidateVault(signal.VaultID)
	})

	accounts := account.NewService(account.Deps{
		Users:         repos.Users,
		Members:       repos.Members,
		VaultKeys:     repos.Keys,
		Bus:           infra.Bus,
		Tokens:        repos.UserTokens,
		RefreshTokens: repos.RefreshTokens,
		Outbox:        outbox,
		Hasher:        hasher,
		Blobs:         repos.Blobs,
		BlobQueue:     repos.BlobQueue,
		Avatars:       avatar.NewProcessor(cfg.Avatar.MaxBytes, cfg.Avatar.Size),
		Tx:            repos.Tx,
		PublicURL:     cfg.Server.PublicURL,
		Verification:  features.EmailVerification,
	})

	var onRegistered func(context.Context, *domain.User)
	if features.EmailVerification {
		onRegistered = func(ctx context.Context, user *domain.User) {
			if err := accounts.SendEmailVerification(ctx, user.ID); err != nil {
				log.Warn().Err(err).Msg("failed to queue verification email")
			}
		}
	}
	authService := auth.NewService(auth.Deps{
		Users:            repos.Users,
		Tx:               repos.Tx,
		Devices:          repos.Devices,
		RefreshTokens:    repos.RefreshTokens,
		Hasher:           hasher,
		Tokens:           tokens,
		RefreshExpiry:    cfg.Auth.RefreshTokenExpiry,
		RegistrationMode: cfg.Auth.RegistrationMode,
		PreloginSecret:   []byte(cfg.Auth.AccessTokenSecret),
		OnRegistered:     onRegistered,
		DevOps:           devOps,
	})
	devices := device.NewService(device.Deps{Devices: repos.Devices, RefreshTokens: repos.RefreshTokens})

	vaults := vault.NewService(vault.Deps{
		Vaults:         repos.Vaults,
		Members:        repos.Members,
		Keys:           repos.Keys,
		Users:          repos.Users,
		Snapshots:      repos.Snapshots,
		Shares:         repos.Shares,
		BlobQueue:      repos.BlobQueue,
		Tx:             repos.Tx,
		Authz:          authorizer,
		CreationPolicy: features.VaultCreation,
		QuotaManaged:   features.MultiTenant(),
	})
	members := vault.NewMemberService(vault.MemberDeps{
		Members: repos.Members, Keys: repos.Keys, Users: repos.Users, Access: repos.Access, Tx: repos.Tx, Bus: infra.Bus,
		Authz: authorizer, GuestsEnabled: features.Guests,
	})
	accessRules := vault.NewAccessService(vault.AccessDeps{
		Access: repos.Access, Members: repos.Members, Tx: repos.Tx, Authz: authorizer, Bus: infra.Bus,
	})
	invites := vault.NewInviteService(vault.InviteDeps{
		Invites: repos.Invites, Members: repos.Members, Keys: repos.Keys, Users: repos.Users, Vaults: repos.Vaults,
		Access: repos.Access, Tx: repos.Tx, Outbox: outbox, Bus: infra.Bus, Authz: authorizer,
		PublicURL: cfg.Server.PublicURL, GuestsEnabled: features.Guests,
	})
	keys := vault.NewKeyService(vault.KeyDeps{Keys: repos.Keys, Members: repos.Members, Users: repos.Users, Bus: infra.Bus, Authz: authorizer})
	encryption := vault.NewEncryptionService(vault.EncryptionDeps{Encryptions: repos.Encryption, Keys: repos.Keys, Members: repos.Members, Authz: authorizer})

	events := realtime.NewEventBroker(64)
	var publisher file.EventPublisher
	if infra.Pool == nil {
		publisher = events
	}
	var shareEntitlement func(ctx context.Context, vaultID uuid.UUID) error
	shares := share.NewService(share.Deps{
		Shares:       repos.Shares,
		Vaults:       repos.Vaults,
		Files:        repos.Latest,
		Blobs:        repos.Blobs,
		BlobQueue:    repos.BlobQueue,
		Tx:           repos.Tx,
		Authz:        authorizer,
		MaxBytes:     cfg.Sync.MaxFileSize,
		EnforceQuota: features.MultiTenant(),
		Entitled: func(ctx context.Context, vaultID uuid.UUID) error {
			if shareEntitlement == nil {
				return nil
			}
			return shareEntitlement(ctx, vaultID)
		},
	})
	templates := template.NewService(template.Deps{
		Templates: repos.Templates,
		Groups:    repos.Access,
		Users:     repos.Users,
		Vaults:    repos.Vaults,
		Tx:        repos.Tx,
		Authz:     authorizer,
	})
	files := file.NewService(file.Deps{
		Snapshots:  repos.Snapshots,
		Deltas:     repos.Deltas,
		Latest:     repos.Latest,
		Events:     repos.Events,
		Members:    repos.Members,
		Users:      repos.Users,
		Devices:    repos.Devices,
		Vaults:     repos.Vaults,
		Blobs:      repos.Blobs,
		BlobQueue:  repos.BlobQueue,
		Tx:         repos.Tx,
		Authz:      authorizer,
		Publisher:  publisher,
		CollabDocs: repos.CollabDocs,
		Shares:     shares,
		Access:     accessRules,
		DeltaPolicy: &file.DeltaPolicy{
			MaxDeltasBeforeSnapshot: cfg.Sync.MaxDeltasBeforeSnapshot,
			MaxDeltaSizeRatio:       cfg.Sync.MaxDeltaSizeRatio,
		},
		MaxFileSize:         cfg.Sync.MaxFileSize,
		MaxSnapshotsPerFile: cfg.Sync.MaxSnapshotsPerFile,
		EnforceQuota:        features.MultiTenant(),
	})

	infra.Bus.Subscribe(bus.KindSSE, func(signal bus.Signal) {
		events.Publish(signal.VaultID, realtime.Event{EventType: signal.Event, Data: string(signal.Data)})
	})
	infra.Bus.Subscribe(bus.KindAccessChanged, func(signal bus.Signal) {
		events.DisconnectUser(signal.VaultID, signal.UserID)
	})
	infra.Bus.Subscribe(bus.KindVaultAccessChanged, func(signal bus.Signal) {
		events.Publish(signal.VaultID, realtime.Event{EventType: bus.VaultEventAccessChanged, Data: "{}"})
	})

	var subscriptionService *subscription.Service
	var subscriptionHandler *subscription.Handler
	var entitlement func(http.Handler) http.Handler
	var vaultEntitlement func(http.Handler) http.Handler
	var entitlementChecker *subscription.EntitlementChecker
	var guard realtime.StreamGuard
	if features.Subscription {
		entitlementChecker = subscription.NewEntitlementChecker(repos.Subscriptions, cfg.Subscription.CacheTTL)
		subscriptionService = subscription.NewService(subscription.Deps{
			Subscriptions: repos.Subscriptions,
			Gateway:       subscription.NewClient(cfg.Subscription.StripeSecretKey, cfg.Subscription.StripePriceID),
			Users:         repos.Users,
			PlanProduct:   cfg.Subscription.StripePriceID,
			RenewalGrace:  cfg.Subscription.RenewalGrace,
			DevOps:        devOps,
		})
		subscriptionHandler = subscription.NewHandler(subscriptionService, subscription.WebhookSecurity{StripeWebhookSecret: cfg.Subscription.StripeWebhookSecret}, entitlementChecker)
		entitlement = entitlementChecker.Middleware
		vaultEntitlement = entitlement
		var owners *vaultOwners
		if features.MultiTenant() {
			owners = newVaultOwners(repos.Vaults)
			vaultEntitlement = entitlementChecker.MiddlewareFor(owners.resolver())
		}
		guard = streamGuard(entitlementChecker, owners)
		ownerOfVault := owners
		if ownerOfVault == nil {
			ownerOfVault = newVaultOwners(repos.Vaults)
		}
		shareEntitlement = func(ctx context.Context, vaultID uuid.UUID) error {
			owner, err := ownerOfVault.ownerOf(ctx, vaultID)
			if err != nil {
				return err
			}
			return entitlementChecker.Check(ctx, owner)
		}
	}

	var collab *realtime.CollabHandler
	var presence CollabPresenceRepository
	if features.Collab {
		presence = repos.Presence
		collab = realtime.NewCollabHandler(realtime.CollabDeps{
			Broker:        realtime.NewRoomBroker(cfg.Collab.MaxPeersPerRoom, cfg.Collab.MaxBufBytes),
			Documents:     repos.CollabDocs,
			Presence:      repos.Presence,
			Members:       repos.Members,
			Users:         repos.Users,
			Bus:           infra.Bus,
			Tickets:       realtime.NewTicketSigner(cfg.Auth.AccessTokenSecret, time.Minute),
			Lifecycle:     life,
			FlushInterval: cfg.Collab.FlushInterval,
			Authz:         authorizer,
			Guard:         guard,
		})
	}

	s := &Server{cfg: cfg, features: features, infra: infra, lifecycle: life, events: events, shares: shares, templates: templates}
	s.handler = newRouter(cfg, features, routes{
		auth:             auth.NewHandler(authService),
		account:          account.NewHandler(accounts),
		device:           device.NewHandler(devices),
		vault:            vault.NewHandler(vaults),
		member:           vault.NewMemberHandler(members),
		access:           vault.NewAccessHandler(accessRules),
		invite:           vault.NewInviteHandler(invites),
		key:              vault.NewKeyHandler(keys),
		encryption:       vault.NewEncryptionHandler(encryption),
		file:             file.NewHandler(files),
		share:            share.NewHandler(shares, cfg.Server.PublicURL),
		template:         template.NewHandler(templates),
		sse:              realtime.NewSSEHandler(realtime.SSEDeps{Broker: events, Events: repos.Events, Members: repos.Members, Authz: authorizer, Lifecycle: life, Guard: guard}),
		collab:           collab,
		readiness:        NewReadinessHandler(infra.Pool, repos.Blobs, life),
		subscription:     subscriptionHandler,
		entitlement:      entitlement,
		vaultEntitlement: vaultEntitlement,
		tokens:           tokens,
		overview: &OverviewHandler{
			accounts:      accounts,
			devices:       devices,
			vaults:        vaults,
			members:       members,
			invites:       invites,
			access:        accessRules,
			subscriptions: subscriptionService,
			authz:         authorizer,
			deviceStore:   repos.Devices,
			memberStore:   repos.Members,
			presence:      presence,
		},
	})

	if infra.Pool != nil {
		s.listener = postgres.NewListener(infra.Pool)
		s.listener.Handle("sync_events", postgres.SyncEventHandler(repos.Events, events.PublishSyncEvent))
		s.listener.Handle(postgres.SignalChannel, infra.PGBus.Handler())
		if entitlementChecker != nil {
			s.listener.Handle("subscription_updates", postgres.SubscriptionInvalidationHandler(entitlementChecker.Invalidate))
		}
	}
	return s
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Start(ctx context.Context) {
	if s.listener != nil {
		go s.listener.Run(ctx)
	}
	startBackgroundJobs(ctx, s.cfg, s.features, s.infra, s.shares, s.templates)
}

func (s *Server) Shutdown(srv *http.Server) {
	log.Info().Msg("draining server")
	s.lifecycle.StartDrain()
	time.Sleep(s.cfg.Server.DrainDelay)

	if !s.lifecycle.CloseStreams(s.cfg.Server.ShutdownTimeout) {
		log.Warn().Msg("realtime streams did not close before timeout")
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown")
	}
}

func NewInfra(ctx context.Context, cfg *config.Config) (Infra, func(), error) {
	mailer, err := newMailer(cfg)
	if err != nil {
		return Infra{}, nil, err
	}
	if cfg.UseFakeRepos {
		log.Info().Msg("using in-memory fake repositories")
		return Infra{Repos: FakeRepositories(), Bus: bus.NewMemory(), Mailer: mailer}, func() {}, nil
	}

	pool, err := postgres.NewPool(ctx, cfg.Database)
	if err != nil {
		return Infra{}, nil, fmt.Errorf("connecting to database: %w", err)
	}
	blobs, err := s3.NewBlobStorage(ctx, cfg.S3)
	if err != nil {
		pool.Close()
		return Infra{}, nil, fmt.Errorf("connecting to object storage: %w", err)
	}
	pgBus := postgres.NewSignalBus(pool)
	return Infra{
		Pool:   pool,
		Repos:  PostgresRepositories(pool, blobs),
		Bus:    pgBus,
		PGBus:  pgBus,
		Mailer: mailer,
	}, pool.Close, nil
}

func newMailer(cfg *config.Config) (Mailer, error) {
	switch cfg.Mail.Driver {
	case "none":
		log.Info().Msg("mail disabled")
		return nil, nil
	case "log":
		renderer, err := mail.NewRenderer()
		renderer = renderer.WithPublicURL(cfg.Server.PublicURL)
		if err != nil {
			return nil, err
		}
		log.Info().Msg("mail driver: log")
		return mail.NewLogMailer(renderer), nil
	default:
		renderer, err := mail.NewRenderer()
		renderer = renderer.WithPublicURL(cfg.Server.PublicURL)
		if err != nil {
			return nil, err
		}
		mailer, err := mail.NewSMTPMailer(mail.SMTPConfig{
			Host:     cfg.Mail.Host,
			Port:     cfg.Mail.Port,
			Username: cfg.Mail.Username,
			Password: cfg.Mail.Password,
			From:     cfg.Mail.From,
			TLSMode:  cfg.Mail.TLSMode,
			Timeout:  cfg.Mail.Timeout,
		}, renderer)
		if err != nil {
			return nil, err
		}
		log.Info().Str("host", cfg.Mail.Host).Msg("mail driver: smtp")
		return mailer, nil
	}
}

func Run(cfg *config.Config) error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	infra, closeInfra, err := NewInfra(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		stop()
		closeInfra()
	}()

	s := New(ctx, cfg, infra)
	s.Start(ctx)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", addr).Str("version", Version).Msg("starting server")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		return err
	case <-shutdown:
	}

	s.Shutdown(srv)
	stop()
	log.Info().Msg("server stopped")
	return nil
}

type vaultOwners struct {
	vaults VaultRepository
	mu     sync.RWMutex
	owners map[uuid.UUID]uuid.UUID
}

func newVaultOwners(vaults VaultRepository) *vaultOwners {
	return &vaultOwners{vaults: vaults, owners: make(map[uuid.UUID]uuid.UUID)}
}

func (o *vaultOwners) ownerOf(ctx context.Context, vaultID uuid.UUID) (uuid.UUID, error) {
	o.mu.RLock()
	owner, ok := o.owners[vaultID]
	o.mu.RUnlock()
	if ok {
		return owner, nil
	}
	v, err := o.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return uuid.Nil, err
	}
	o.mu.Lock()
	if len(o.owners) > 100_000 {
		o.owners = make(map[uuid.UUID]uuid.UUID)
	}
	o.owners[vaultID] = v.OwnerID
	o.mu.Unlock()
	return v.OwnerID, nil
}

func (o *vaultOwners) resolver() subscription.SubjectResolver {
	return func(r *http.Request) (uuid.UUID, error) {
		vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
		if err != nil {
			return httpx.ClaimsFromContext(r.Context()).UserID, nil
		}
		return o.ownerOf(r.Context(), vaultID)
	}
}

func streamGuard(checker *subscription.EntitlementChecker, owners *vaultOwners) realtime.StreamGuard {
	return func(ctx context.Context, vaultID, userID uuid.UUID) error {
		subject := userID
		if owners != nil {
			owner, err := owners.ownerOf(ctx, vaultID)
			if err != nil {
				return err
			}
			subject = owner
		}
		return checker.Check(ctx, subject)
	}
}
