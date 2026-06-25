package postgres

import (
	"errors"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case uniqueViolation:
			return domain.ErrAlreadyExists
		case foreignKeyViolation:
			return domain.ErrNotFound
		}
	}

	return err
}
