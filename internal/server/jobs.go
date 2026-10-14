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

func startBackgroundJobs(ctx context.Context, cfg *config.Config, features feature.Set, infra Infra, shares *share.Service, templates *template.Service) {
	repos := infra.Repos

	go job.NewCleaner(job.CleanerConfig{
		Interval: time.Hour,
		Tx:       repos.Tx,
		Locker:   repos.Locker,
		LockKey:  maintenanceLockKey,
		Tasks: []job.CleanupTask{
			job.ExpiredRefreshTokens(repos.RefreshTokens),
			job.ExpiredInvites(repos.Invites),
			job.OldSyncEvents(repos.Events, cfg.Sync.EventRetention),
			{Name: "user_tokens", Run: repos.UserTokens.DeleteExpired},
			{Name: "expired_shares", Run: shares.RemoveExpired},
			{Name: "template_review", Run: templates.ReviewPending},
		},
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
			Locker:   repos.Locker,
			LockKey:  realtimeLockKey,
			Tasks:    realtimeTasks,
		}).Run(ctx)
	}

	if repos.BlobQueue != nil {
		go job.NewBlobJanitor(repos.Tx, repos.BlobQueue, repos.Blobs, time.Minute).Run(ctx)
	}

	if infra.Mailer != nil {
		go job.NewMailWorker(repos.Tx, repos.Outbox, infra.Mailer, 3*time.Second).Run(ctx)
	} else {
		log.Info().Msg("mail worker disabled")
	}
}
