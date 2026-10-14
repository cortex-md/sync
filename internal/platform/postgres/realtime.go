package postgres

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CollabDocumentRepository struct {
	store
	tx *Transactor
}

func NewCollabDocumentRepository(pool *pgxpool.Pool) *CollabDocumentRepository {
	return &CollabDocumentRepository{store: store{pool}, tx: NewTransactor(pool)}
}

func (r *CollabDocumentRepository) StoreUpdate(ctx context.Context, update *domain.CollabUpdate) error {
	return r.tx.RunInTx(ctx, func(ctx context.Context) error {
		q := r.q(ctx)
		row, err := q.InsertCollabUpdate(ctx, db.InsertCollabUpdateParams{VaultID: update.VaultID, FilePath: update.FilePath, Data: update.Data})
		if err != nil {
			return mapError(err)
		}
		update.ID, update.CreatedAt = row.ID, row.CreatedAt
		return mapError(q.BumpCollabDocument(ctx, db.BumpCollabDocumentParams{VaultID: update.VaultID, FilePath: update.FilePath, Added: 1}))
	})
}

func (r *CollabDocumentRepository) BatchStoreUpdates(ctx context.Context, vaultID uuid.UUID, filePath string, updates [][]byte) error {
	if len(updates) == 0 {
		return nil
	}
	return r.tx.RunInTx(ctx, func(ctx context.Context) error {
		tx, ok := ctx.Value(txKey).(pgx.Tx)
		if !ok {
			return errNotFound
		}
		rows := make([][]any, len(updates))
		for i, data := range updates {
			rows[i] = []any{vaultID, filePath, data}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"collab_updates"}, []string{"vault_id", "file_path", "data"}, pgx.CopyFromRows(rows)); err != nil {
			return mapError(err)
		}
		return mapError(r.q(ctx).BumpCollabDocument(ctx, db.BumpCollabDocumentParams{VaultID: vaultID, FilePath: filePath, Added: int32(len(updates))}))
	})
}

func (r *CollabDocumentRepository) LoadDocument(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.CollabDocument, []domain.CollabUpdate, error) {
	q := r.q(ctx)
	doc, err := q.GetCollabDocument(ctx, db.GetCollabDocumentParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		if mapError(err) == errNotFound {
			return nil, nil, nil
		}
		return nil, nil, mapError(err)
	}
	rows, err := q.ListCollabUpdates(ctx, db.ListCollabUpdatesParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, nil, mapError(err)
	}
	updates := make([]domain.CollabUpdate, len(rows))
	for i, u := range rows {
		updates[i] = domain.CollabUpdate{ID: u.ID, VaultID: u.VaultID, FilePath: u.FilePath, Data: u.Data, CreatedAt: u.CreatedAt}
	}
	return &domain.CollabDocument{
		VaultID:        doc.VaultID,
		FilePath:       doc.FilePath,
		CompactedState: doc.CompactedState,
		StateVector:    doc.StateVector,
		UpdateCount:    int(doc.UpdateCount),
		UpdatedAt:      doc.UpdatedAt,
	}, updates, nil
}

func (r *CollabDocumentRepository) CompactDocument(ctx context.Context, vaultID uuid.UUID, filePath string, compactedState []byte, upToUpdateID int64) error {
	return r.tx.RunInTx(ctx, func(ctx context.Context) error {
		q := r.q(ctx)
		removed, err := q.DeleteCollabUpdatesUpTo(ctx, db.DeleteCollabUpdatesUpToParams{VaultID: vaultID, FilePath: filePath, UpTo: upToUpdateID})
		if err != nil {
			return mapError(err)
		}
		return mapError(q.SaveCompactedCollabDocument(ctx, db.SaveCompactedCollabDocumentParams{
			VaultID: vaultID, FilePath: filePath, CompactedState: compactedState, Removed: int32(removed),
		}))
	})
}

func (r *CollabDocumentRepository) DeleteForPath(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	q := r.q(ctx)
	if err := q.DeleteCollabUpdates(ctx, db.DeleteCollabUpdatesParams{VaultID: vaultID, FilePath: filePath}); err != nil {
		return mapError(err)
	}
	_, err := q.DeleteCollabDocument(ctx, db.DeleteCollabDocumentParams{VaultID: vaultID, FilePath: filePath})
	return mapError(err)
}

func (r *CollabDocumentRepository) MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	q := r.q(ctx)
	if err := q.DeleteCollabUpdates(ctx, db.DeleteCollabUpdatesParams{VaultID: vaultID, FilePath: newPath}); err != nil {
		return mapError(err)
	}
	if _, err := q.DeleteCollabDocument(ctx, db.DeleteCollabDocumentParams{VaultID: vaultID, FilePath: newPath}); err != nil {
		return mapError(err)
	}
	if err := q.MoveCollabDocument(ctx, db.MoveCollabDocumentParams{VaultID: vaultID, OldPath: oldPath, NewPath: newPath}); err != nil {
		return mapError(err)
	}
	return mapError(q.MoveCollabUpdates(ctx, db.MoveCollabUpdatesParams{VaultID: vaultID, OldPath: oldPath, NewPath: newPath}))
}

type CollabPresenceRepository struct{ store }

func NewCollabPresenceRepository(pool *pgxpool.Pool) *CollabPresenceRepository {
	return &CollabPresenceRepository{store{pool}}
}

func toPresence(rows []db.CollabPresence) []domain.CollabPresence {
	result := make([]domain.CollabPresence, len(rows))
	for i, p := range rows {
		result[i] = domain.CollabPresence{
			ClientID: p.ClientID, VaultID: p.VaultID, FilePath: p.FilePath, UserID: p.UserID,
			CanWrite: p.CanWrite, JoinedAt: p.JoinedAt, LastSeenAt: p.LastSeenAt,
		}
	}
	return result
}

func (r *CollabPresenceRepository) Join(ctx context.Context, p *domain.CollabPresence) error {
	return mapError(r.q(ctx).JoinPresence(ctx, db.JoinPresenceParams{
		ClientID: p.ClientID, VaultID: p.VaultID, FilePath: p.FilePath, UserID: p.UserID, CanWrite: p.CanWrite,
	}))
}

func (r *CollabPresenceRepository) Leave(ctx context.Context, clientID string) error {
	return mapError(r.q(ctx).LeavePresence(ctx, clientID))
}

func (r *CollabPresenceRepository) Touch(ctx context.Context, clientIDs []string) error {
	if len(clientIDs) == 0 {
		return nil
	}
	return mapError(r.q(ctx).TouchPresence(ctx, clientIDs))
}

func (r *CollabPresenceRepository) ListRoom(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.CollabPresence, error) {
	rows, err := r.q(ctx).ListRoomPresence(ctx, db.ListRoomPresenceParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	return toPresence(rows), nil
}

func (r *CollabPresenceRepository) ListVault(ctx context.Context, vaultID uuid.UUID) ([]domain.CollabPresence, error) {
	rows, err := r.q(ctx).ListVaultPresence(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	return toPresence(rows), nil
}

func (r *CollabPresenceRepository) DeleteStale(ctx context.Context, before time.Time) (int64, error) {
	n, err := r.q(ctx).DeleteStalePresence(ctx, before)
	return n, mapError(err)
}
