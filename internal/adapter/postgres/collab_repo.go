package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CollabDocumentRepository struct {
	pool *pgxpool.Pool
}

func NewCollabDocumentRepository(pool *pgxpool.Pool) *CollabDocumentRepository {
	return &CollabDocumentRepository{pool: pool}
}

func (r *CollabDocumentRepository) BatchStoreUpdates(ctx context.Context, vaultID uuid.UUID, filePath string, updates [][]byte) error {
	if len(updates) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	rows := make([][]interface{}, len(updates))
	for i, data := range updates {
		rows[i] = []interface{}{vaultID, filePath, data}
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"collab_updates"},
		[]string{"vault_id", "file_path", "data"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return mapError(err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO collab_documents (vault_id, file_path, update_count, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (vault_id, file_path) DO UPDATE SET
		   update_count = collab_documents.update_count + $3,
		   updated_at = NOW()`,
		vaultID, filePath, len(updates),
	)
	if err != nil {
		return mapError(err)
	}

	return tx.Commit(ctx)
}

func (r *CollabDocumentRepository) StoreUpdate(ctx context.Context, update *domain.CollabUpdate) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx,
		`INSERT INTO collab_updates (vault_id, file_path, data, created_at)
		 VALUES ($1, $2, $3, NOW())
		 RETURNING id, created_at`,
		update.VaultID, update.FilePath, update.Data,
	).Scan(&update.ID, &update.CreatedAt)
	if err != nil {
		return mapError(err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO collab_documents (vault_id, file_path, update_count, updated_at)
		 VALUES ($1, $2, 1, NOW())
		 ON CONFLICT (vault_id, file_path) DO UPDATE SET
		   update_count = collab_documents.update_count + 1,
		   updated_at = NOW()`,
		update.VaultID, update.FilePath,
	)
	if err != nil {
		return mapError(err)
	}

	return tx.Commit(ctx)
}

func (r *CollabDocumentRepository) LoadDocument(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.CollabDocument, []domain.CollabUpdate, error) {
	var doc domain.CollabDocument
	err := r.pool.QueryRow(ctx,
		`SELECT vault_id, file_path, COALESCE(compacted_state, ''::bytea), COALESCE(state_vector, ''::bytea), update_count, updated_at
		 FROM collab_documents WHERE vault_id = $1 AND file_path = $2`,
		vaultID, filePath,
	).Scan(&doc.VaultID, &doc.FilePath, &doc.CompactedState, &doc.StateVector, &doc.UpdateCount, &doc.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, mapError(err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id, vault_id, file_path, data, created_at
		 FROM collab_updates WHERE vault_id = $1 AND file_path = $2 ORDER BY id`,
		vaultID, filePath,
	)
	if err != nil {
		return nil, nil, mapError(err)
	}
	defer rows.Close()

	var updates []domain.CollabUpdate
	for rows.Next() {
		var u domain.CollabUpdate
		if err := rows.Scan(&u.ID, &u.VaultID, &u.FilePath, &u.Data, &u.CreatedAt); err != nil {
			return nil, nil, mapError(err)
		}
		updates = append(updates, u)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapError(err)
	}

	return &doc, updates, nil
}

func (r *CollabDocumentRepository) CompactDocument(ctx context.Context, vaultID uuid.UUID, filePath string, compactedState []byte, stateVector []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO collab_documents (vault_id, file_path, compacted_state, state_vector, update_count, updated_at)
		 VALUES ($1, $2, $3, $4, 0, NOW())
		 ON CONFLICT (vault_id, file_path) DO UPDATE SET
		   compacted_state = EXCLUDED.compacted_state,
		   state_vector = EXCLUDED.state_vector,
		   update_count = 0,
		   updated_at = NOW()`,
		vaultID, filePath, compactedState, stateVector,
	)
	if err != nil {
		return mapError(err)
	}

	_, err = tx.Exec(ctx,
		`DELETE FROM collab_updates WHERE vault_id = $1 AND file_path = $2`,
		vaultID, filePath,
	)
	if err != nil {
		return mapError(err)
	}

	return tx.Commit(ctx)
}

func (r *CollabDocumentRepository) DeleteDocument(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`DELETE FROM collab_documents WHERE vault_id = $1 AND file_path = $2`,
		vaultID, filePath,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	_, err = tx.Exec(ctx,
		`DELETE FROM collab_updates WHERE vault_id = $1 AND file_path = $2`,
		vaultID, filePath,
	)
	if err != nil {
		return mapError(err)
	}

	return tx.Commit(ctx)
}
