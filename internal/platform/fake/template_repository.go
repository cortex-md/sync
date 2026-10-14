package fake

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type TemplateRepository struct {
	mu        sync.Mutex
	templates map[uuid.UUID]*domain.TemplateShare
	users     *UserRepository
	vaults    *VaultRepository
}

func NewTemplateRepository() *TemplateRepository {
	return &TemplateRepository{templates: make(map[uuid.UUID]*domain.TemplateShare)}
}

func (r *TemplateRepository) ResolveListingsWith(users *UserRepository, vaults *VaultRepository) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = users
	r.vaults = vaults
}

func copyTemplate(t *domain.TemplateShare) domain.TemplateShare {
	out := *t
	out.Payload = append([]byte(nil), t.Payload...)
	return out
}

func (r *TemplateRepository) Create(_ context.Context, t *domain.TemplateShare) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.templates {
		if existing.Slug == t.Slug || existing.ID == t.ID {
			return domain.ErrAlreadyExists
		}
	}
	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now
	stored := copyTemplate(t)
	r.templates[t.ID] = &stored
	return nil
}

func (r *TemplateRepository) Get(_ context.Context, id uuid.UUID) (*domain.TemplateShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.templates[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := copyTemplate(t)
	return &out, nil
}

func (r *TemplateRepository) GetForUpdate(ctx context.Context, id uuid.UUID) (*domain.TemplateShare, error) {
	return r.Get(ctx, id)
}

func (r *TemplateRepository) GetBySlug(_ context.Context, slug string) (*domain.TemplateShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.templates {
		if t.Slug == slug {
			out := copyTemplate(t)
			return &out, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *TemplateRepository) ListByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.TemplateListing, error) {
	return r.withOwners(ctx, r.list(func(t *domain.TemplateShare) bool { return t.VaultID == vaultID }, 0)), nil
}

func (r *TemplateRepository) ListMarket(ctx context.Context, kind domain.TemplateKind, query string, limit int) ([]domain.TemplateListing, error) {
	needle := strings.ToLower(query)
	return r.withOwners(ctx, r.list(func(t *domain.TemplateShare) bool {
		if t.Audience != domain.TemplateAudienceMarket || t.Status != domain.TemplateStatusPublished {
			return false
		}
		if kind != "" && t.Kind != kind {
			return false
		}
		if needle != "" && !strings.Contains(strings.ToLower(t.Name), needle) && !strings.Contains(strings.ToLower(t.Description), needle) {
			return false
		}
		if r.vaults == nil {
			return true
		}
		vault, err := r.vaults.GetByID(ctx, t.VaultID)
		return err == nil && vault.AllowPublicLinks
	}, limit)), nil
}

func (r *TemplateRepository) withOwners(ctx context.Context, templates []domain.TemplateShare) []domain.TemplateListing {
	listings := make([]domain.TemplateListing, len(templates))
	for i, t := range templates {
		listings[i].TemplateShare = t
		if r.users == nil || t.OwnerID == nil {
			continue
		}
		if user, err := r.users.GetByID(ctx, *t.OwnerID); err == nil {
			listings[i].OwnerName = user.DisplayName
		}
	}
	return listings
}

func (r *TemplateRepository) list(match func(*domain.TemplateShare) bool, limit int) []domain.TemplateShare {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := []domain.TemplateShare{}
	for _, t := range r.templates {
		if match(t) {
			result = append(result, copyTemplate(t))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].UpdatedAt.After(result[j].UpdatedAt)
		}
		return result[i].ID.String() < result[j].ID.String()
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (r *TemplateRepository) Update(_ context.Context, t *domain.TemplateShare) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.templates[t.ID]; !ok {
		return domain.ErrNotFound
	}
	t.UpdatedAt = time.Now()
	stored := copyTemplate(t)
	r.templates[t.ID] = &stored
	return nil
}

func (r *TemplateRepository) SetReview(_ context.Context, id uuid.UUID, version int, status domain.TemplateStatus, note string, fileCount int) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.templates[id]
	if !ok || t.Version != version || t.Status != domain.TemplateStatusPendingReview {
		return false, nil
	}
	t.Status, t.ReviewNote, t.FileCount, t.UpdatedAt = status, note, fileCount, time.Now()
	return true, nil
}

func (r *TemplateRepository) ListPending(_ context.Context, limit int) ([]domain.TemplateShare, error) {
	return r.list(func(t *domain.TemplateShare) bool { return t.Status == domain.TemplateStatusPendingReview }, limit), nil
}

func (r *TemplateRepository) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.templates[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.templates, id)
	return nil
}
