package postgres

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	pool *pgxpool.Pool
}

func (s store) q(ctx context.Context) *db.Queries {
	return db.New(connFromContext(ctx, s.pool))
}

func expectRows(rows int64, err error) error {
	if err != nil {
		return mapError(err)
	}
	if rows == 0 {
		return errNotFound
	}
	return nil
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
