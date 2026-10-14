package postgres

import (
	"context"
	"strings"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TemplateRepository struct{ store }

func NewTemplateRepository(pool *pgxpool.Pool) *TemplateRepository {
	return &TemplateRepository{store{pool}}
}

func toTemplate(t db.TemplateShare) domain.TemplateShare {
	return domain.TemplateShare{
		ID:             t.ID,
		VaultID:        t.VaultID,
		OwnerID:        t.OwnerID,
		Kind:           domain.TemplateKind(t.Kind),
		Audience:       domain.TemplateAudience(t.Audience),
		GroupID:        t.GroupID,
		Name:           t.Name,
		Description:    t.Description,
		IncludeSamples: t.IncludeSamples,
		Payload:        t.Payload,
		Encrypted:      t.Encrypted,
		FileCount:      int(t.FileCount),
		Version:        int(t.Version),
		Status:         domain.TemplateStatus(t.Status),
		Slug:           t.Slug,
		ReviewNote:     t.ReviewNote,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
	}
}

func toTemplateListing(t db.ListTemplateSharesByVaultRow) domain.TemplateListing {
	return domain.TemplateListing{TemplateShare: domain.TemplateShare{
		ID:             t.ID,
		VaultID:        t.VaultID,
		OwnerID:        t.OwnerID,
		Kind:           domain.TemplateKind(t.Kind),
		Audience:       domain.TemplateAudience(t.Audience),
		GroupID:        t.GroupID,
		Name:           t.Name,
		Description:    t.Description,
		IncludeSamples: t.IncludeSamples,
		Encrypted:      t.Encrypted,
		FileCount:      int(t.FileCount),
		Version:        int(t.Version),
		Status:         domain.TemplateStatus(t.Status),
		Slug:           t.Slug,
		ReviewNote:     t.ReviewNote,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
	}, OwnerName: t.OwnerName}
}

func (r *TemplateRepository) Create(ctx context.Context, t *domain.TemplateShare) error {
	row, err := r.q(ctx).CreateTemplateShare(ctx, db.CreateTemplateShareParams{
		ID:             t.ID,
		VaultID:        t.VaultID,
		OwnerID:        t.OwnerID,
		Kind:           string(t.Kind),
		Audience:       string(t.Audience),
		GroupID:        t.GroupID,
		Name:           t.Name,
		Description:    t.Description,
		IncludeSamples: t.IncludeSamples,
		Payload:        t.Payload,
		Encrypted:      t.Encrypted,
		FileCount:      int32(t.FileCount),
		Version:        int32(t.Version),
		Status:         string(t.Status),
		Slug:           t.Slug,
		ReviewNote:     t.ReviewNote,
	})
	if err != nil {
		return mapError(err)
	}
	t.CreatedAt, t.UpdatedAt = row.CreatedAt, row.UpdatedAt
	return nil
}

func (r *TemplateRepository) Get(ctx context.Context, id uuid.UUID) (*domain.TemplateShare, error) {
	row, err := r.q(ctx).GetTemplateShare(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	t := toTemplate(row)
	return &t, nil
}

func (r *TemplateRepository) GetForUpdate(ctx context.Context, id uuid.UUID) (*domain.TemplateShare, error) {
	row, err := r.q(ctx).GetTemplateShareForUpdate(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	t := toTemplate(row)
	return &t, nil
}

func (r *TemplateRepository) GetBySlug(ctx context.Context, slug string) (*domain.TemplateShare, error) {
	row, err := r.q(ctx).GetTemplateShareBySlug(ctx, slug)
	if err != nil {
		return nil, mapError(err)
	}
	t := toTemplate(row)
	return &t, nil
}

func (r *TemplateRepository) ListByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.TemplateListing, error) {
	rows, err := r.q(ctx).ListTemplateSharesByVault(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	templates := make([]domain.TemplateListing, len(rows))
	for i, row := range rows {
		templates[i] = toTemplateListing(row)
	}
	return templates, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r *TemplateRepository) ListMarket(ctx context.Context, kind domain.TemplateKind, query string, limit int) ([]domain.TemplateListing, error) {
	params := db.ListMarketTemplateSharesParams{RowLimit: int32(limit)}
	if kind != "" {
		value := string(kind)
		params.Kind = &value
	}
	if query != "" {
		pattern := "%" + likeEscaper.Replace(query) + "%"
		params.Pattern = &pattern
	}
	rows, err := r.q(ctx).ListMarketTemplateShares(ctx, params)
	if err != nil {
		return nil, mapError(err)
	}
	templates := make([]domain.TemplateListing, len(rows))
	for i, row := range rows {
		templates[i] = toTemplateListing(db.ListTemplateSharesByVaultRow(row))
	}
	return templates, nil
}

func (r *TemplateRepository) Update(ctx context.Context, t *domain.TemplateShare) error {
	updatedAt, err := r.q(ctx).UpdateTemplateShare(ctx, db.UpdateTemplateShareParams{
		ID:             t.ID,
		Audience:       string(t.Audience),
		GroupID:        t.GroupID,
		Name:           t.Name,
		Description:    t.Description,
		IncludeSamples: t.IncludeSamples,
		Payload:        t.Payload,
		Encrypted:      t.Encrypted,
		FileCount:      int32(t.FileCount),
		Version:        int32(t.Version),
		Status:         string(t.Status),
		ReviewNote:     t.ReviewNote,
	})
	if err != nil {
		return mapError(err)
	}
	t.UpdatedAt = updatedAt
	return nil
}

func (r *TemplateRepository) SetReview(ctx context.Context, id uuid.UUID, version int, status domain.TemplateStatus, note string, fileCount int) (bool, error) {
	rows, err := r.q(ctx).SetTemplateShareReview(ctx, db.SetTemplateShareReviewParams{
		ID: id, Version: int32(version), Status: string(status), ReviewNote: note, FileCount: int32(fileCount),
	})
	if err != nil {
		return false, mapError(err)
	}
	return rows > 0, nil
}

func (r *TemplateRepository) ListPending(ctx context.Context, limit int) ([]domain.TemplateShare, error) {
	rows, err := r.q(ctx).ListPendingTemplateShares(ctx, int32(limit))
	if err != nil {
		return nil, mapError(err)
	}
	templates := make([]domain.TemplateShare, len(rows))
	for i, row := range rows {
		templates[i] = toTemplate(row)
	}
	return templates, nil
}

func (r *TemplateRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return expectRows(r.q(ctx).DeleteTemplateShare(ctx, id))
}
