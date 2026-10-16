package server

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/job"
	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/feature"
	"github.com/cortexnotes/cortex-sync/internal/share"
	"github.com/cortexnotes/cortex-sync/internal/template"
	"github.com/rs/zerolog/log"
)

const (
	maintenanceLockKey int64 = 0x636f727465780001
	realtimeLockKey    int64 = 0x636f727465780002
	presenceTTL              = 90 * time.Second
	relayRetention           = 5 * time.Minute
)

func startBackgroundJobs(ctx context.Context, cfg *config.Config, features feature.Set, infra Infra, shares *share.Service, templates *template.Service, janitor *job.BlobJanitor, dispatcher *job.MailDispatcher) {
	repos := infra.Repos
	locker := repos.Locker
	if cfg.Deployment == config.DeploymentSingle {
		locker = nil
	}

	maintenance := []job.CleanupTask{
		job.ExpiredRefreshTokens(repos.RefreshTokens),
		job.ExpiredInvites(repos.Invites),
		job.OldSyncEvents(repos.Events, cfg.Sync.EventRetention),
		{Name: "user_tokens", Run: repos.UserTokens.DeleteExpired},
		{Name: "expired_shares", Run: shares.RemoveExpired},
		{Name: "template_review", Run: templates.ReviewPending},
		job.AtMostEvery(24*time.Hour, job.CleanupTask{Name: "vault_storage_reconcile", Run: repos.Vaults.ReconcileStorage}),
	}
	if dispatcher != nil {
		maintenance = append(maintenance,
			job.CleanupTask{Name: "email_outbox_sweep", Run: dispatcher.Sweep},
			job.CleanupTask{Name: "email_outbox_retention", Run: dispatcher.PruneFinished},
		)
		go dispatcher.Run(ctx)
	} else {
		log.Info().Msg("mail dispatcher disabled")
	}

	go job.NewCleaner(job.CleanerConfig{
		Interval: time.Hour,
		Tx:       repos.Tx,
		Locker:   locker,
		LockKey:  maintenanceLockKey,
		Tasks:    maintenance,
	}).Run(ctx)

	var realtimeTasks []job.CleanupTask
	if features.Collab {
		realtimeTasks = append(realtimeTasks, job.CleanupTask{Name: "collab_presence", Run: func(ctx context.Context) (int64, error) {
			return repos.Presence.DeleteStale(ctx, time.Now().Add(-presenceTTL))
		}})
	}
	if infra.PGBus != nil {
		realtimeTasks = append(realtimeTasks, job.CleanupTask{Name: "realtime_relay", Run: func(ctx context.Context) (int64, error) {
			return infra.PGBus.Prune(ctx, relayRetention)
		}})
	}
	if len(realtimeTasks) > 0 {
		go job.NewCleaner(job.CleanerConfig{
			Interval: time.Minute,
			Tx:       repos.Tx,
			Locker:   locker,
			LockKey:  realtimeLockKey,
			Tasks:    realtimeTasks,
		}).Run(ctx)
	}

	if janitor != nil {
		go janitor.Run(ctx)
	}

}
