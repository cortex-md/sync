package template

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type TemplateRepository interface {
	Create(ctx context.Context, template *domain.TemplateShare) error
	Get(ctx context.Context, id uuid.UUID) (*domain.TemplateShare, error)
	GetForUpdate(ctx context.Context, id uuid.UUID) (*domain.TemplateShare, error)
	GetBySlug(ctx context.Context, slug string) (*domain.TemplateShare, error)
	ListByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.TemplateListing, error)
	ListMarket(ctx context.Context, kind domain.TemplateKind, query string, limit int) ([]domain.TemplateListing, error)
	Update(ctx context.Context, template *domain.TemplateShare) error
	SetReview(ctx context.Context, id uuid.UUID, version int, status domain.TemplateStatus, note string, fileCount int) (bool, error)
	ListPending(ctx context.Context, limit int) ([]domain.TemplateShare, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type GroupLookup interface {
	ListGroups(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultGroup, error)
}

type UserLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

type VaultLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
}
