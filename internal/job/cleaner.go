package job

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/rs/zerolog/log"
)

type Cleaner struct {
	refreshTokens  port.RefreshTokenRepository
	invites        port.VaultInviteRepository
	syncEvents     port.SyncEventRepository
	eventRetention time.Duration
	interval       time.Duration
}

func NewCleaner(
	refreshTokens port.RefreshTokenRepository,
	invites port.VaultInviteRepository,
	interval time.Duration,
) *Cleaner {
	return &Cleaner{
		refreshTokens: refreshTokens,
		invites:       invites,
		interval:      interval,
	}
}

func (c *Cleaner) SetSyncEvents(repo port.SyncEventRepository, retention time.Duration) {
	c.syncEvents = repo
	c.eventRetention = retention
}

func (c *Cleaner) Run(ctx context.Context) {
	c.runOnce(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

func (c *Cleaner) runOnce(ctx context.Context) {
	n, err := c.refreshTokens.DeleteExpired(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cleaner: failed to delete expired refresh tokens")
	} else if n > 0 {
		log.Info().Int64("count", n).Msg("cleaner: deleted expired refresh tokens")
	}

	n, err = c.invites.DeleteExpired(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cleaner: failed to delete expired invites")
	} else if n > 0 {
		log.Info().Int64("count", n).Msg("cleaner: deleted expired invites")
	}

	if c.syncEvents != nil && c.eventRetention > 0 {
		before := time.Now().Add(-c.eventRetention)
		n, err = c.syncEvents.DeleteOlderThan(ctx, before)
		if err != nil {
			log.Error().Err(err).Msg("cleaner: failed to delete old sync events")
		} else if n > 0 {
			log.Info().Int64("count", n).Msg("cleaner: deleted old sync events")
		}
	}
}
