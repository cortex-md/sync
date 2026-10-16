package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/txhook"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKeyType struct{}

var txKey = txKeyType{}

type dbConn interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type Transactor struct {
	pool *pgxpool.Pool
}

func NewTransactor(pool *pgxpool.Pool) *Transactor {
	return &Transactor{pool: pool}
}

const maxTxAttempts = 3

func (t *Transactor) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	var err error
	for attempt := 1; attempt <= maxTxAttempts; attempt++ {
		err = t.runOnce(ctx, fn)
		if !retryable(err) || ctx.Err() != nil {
			return err
		}
		time.Sleep(time.Duration(attempt*attempt) * 10 * time.Millisecond)
	}
	return err
}

func retryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40P01" || pgErr.Code == "40001"
}

func (t *Transactor) runOnce(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := t.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	hookCtx, finish := txhook.Begin(ctx)
	txCtx := context.WithValue(hookCtx, txKey, tx)
	if fnErr := fn(txCtx); fnErr != nil {
		_ = tx.Rollback(ctx)
		return fnErr
	}

	if err := tx.Commit(ctx); err != nil {
		return errors.Join(domain.ErrCommitUncertain, err)
	}
	finish(true)
	return nil
}

func connFromContext(ctx context.Context, pool *pgxpool.Pool) dbConn {
	if tx, ok := ctx.Value(txKey).(pgx.Tx); ok {
		return tx
	}
	return pool
}
