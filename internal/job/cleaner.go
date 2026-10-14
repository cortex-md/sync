package job

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

type CleanupTask struct {
	Name string
	Run  func(ctx context.Context) (int64, error)
}

type CleanerConfig struct {
	Interval time.Duration
	Tasks    []CleanupTask
	Tx       Transactor
	Locker   Locker
	LockKey  int64
}

type Cleaner struct {
	cfg CleanerConfig
}

func NewCleaner(cfg CleanerConfig) *Cleaner {
	return &Cleaner{cfg: cfg}
}

func ExpiredRefreshTokens(repo RefreshTokenRepository) CleanupTask {
	return CleanupTask{Name: "refresh_tokens", Run: repo.DeleteExpired}
}

func ExpiredInvites(repo VaultInviteRepository) CleanupTask {
	return CleanupTask{Name: "invites", Run: repo.DeleteExpired}
}

func OldSyncEvents(repo SyncEventRepository, retention time.Duration) CleanupTask {
	return CleanupTask{Name: "sync_events", Run: func(ctx context.Context) (int64, error) {
		if retention <= 0 {
			return 0, nil
		}
		return repo.DeleteOlderThan(ctx, time.Now().Add(-retention))
	}}
}

func (c *Cleaner) Run(ctx context.Context) {
	c.RunOnce(ctx)

	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.RunOnce(ctx)
		}
	}
}

func (c *Cleaner) RunOnce(ctx context.Context) {
	if c.cfg.Tx == nil || c.cfg.Locker == nil {
		c.clean(ctx)
		return
	}

	err := c.cfg.Tx.RunInTx(ctx, func(lockCtx context.Context) error {
		acquired, err := c.cfg.Locker.TryLock(lockCtx, c.cfg.LockKey)
		if err != nil || !acquired {
			return err
		}
		c.clean(ctx)
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("cleaner: run failed")
	}
}

func (c *Cleaner) clean(ctx context.Context) {
	for _, task := range c.cfg.Tasks {
		n, err := task.Run(ctx)
		if err != nil {
			log.Error().Err(err).Str("task", task.Name).Msg("cleaner: task failed")
		} else if n > 0 {
			log.Info().Int64("count", n).Str("task", task.Name).Msg("cleaner: task done")
		}
	}
}
