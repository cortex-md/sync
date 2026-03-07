package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type VaultRepository struct {
	mu     sync.RWMutex
	vaults map[uuid.UUID]*domain.Vault
}

func NewVaultRepository() *VaultRepository {
	return &VaultRepository{
		vaults: make(map[uuid.UUID]*domain.Vault),
	}
}

func (r *VaultRepository) Create(_ context.Context, vault *domain.Vault) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *vault
	r.vaults[vault.ID] = &stored
	return nil
}

func (r *VaultRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.Vault, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	vault, exists := r.vaults[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *vault
	return &result, nil
}

func (r *VaultRepository) ListByUserID(_ context.Context, userID uuid.UUID) ([]domain.Vault, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.Vault
	for _, v := range r.vaults {
		if v.OwnerID == userID {
			result = append(result, *v)
		}
	}
	return result, nil
}

func (r *VaultRepository) Update(_ context.Context, vault *domain.Vault) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.vaults[vault.ID]; !exists {
		return domain.ErrNotFound
	}
	stored := *vault
	r.vaults[vault.ID] = &stored
	return nil
}

func (r *VaultRepository) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.vaults[id]; !exists {
		return domain.ErrNotFound
	}
	delete(r.vaults, id)
	return nil
}

type VaultMemberRepository struct {
	mu      sync.RWMutex
	members map[string]*domain.VaultMember
}

func NewVaultMemberRepository() *VaultMemberRepository {
	return &VaultMemberRepository{
		members: make(map[string]*domain.VaultMember),
	}
}

func memberKey(vaultID, userID uuid.UUID) string {
	return vaultID.String() + ":" + userID.String()
}

func (r *VaultMemberRepository) Add(_ context.Context, member *domain.VaultMember) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := memberKey(member.VaultID, member.UserID)
	if _, exists := r.members[key]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *member
	r.members[key] = &stored
	return nil
}

func (r *VaultMemberRepository) GetByVaultAndUser(_ context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	member, exists := r.members[memberKey(vaultID, userID)]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *member
	return &result, nil
}

func (r *VaultMemberRepository) ListByVaultID(_ context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultMember
	for _, m := range r.members {
		if m.VaultID == vaultID {
			result = append(result, *m)
		}
	}
	return result, nil
}

func (r *VaultMemberRepository) ListByUserID(_ context.Context, userID uuid.UUID) ([]domain.VaultMember, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultMember
	for _, m := range r.members {
		if m.UserID == userID {
			result = append(result, *m)
		}
	}
	return result, nil
}

func (r *VaultMemberRepository) UpdateRole(_ context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	member, exists := r.members[memberKey(vaultID, userID)]
	if !exists {
		return domain.ErrNotFound
	}
	member.Role = role
	return nil
}

func (r *VaultMemberRepository) Remove(_ context.Context, vaultID, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := memberKey(vaultID, userID)
	if _, exists := r.members[key]; !exists {
		return domain.ErrNotFound
	}
	delete(r.members, key)
	return nil
}

type VaultInviteRepository struct {
	mu      sync.RWMutex
	invites map[uuid.UUID]*domain.VaultInvite
}

func NewVaultInviteRepository() *VaultInviteRepository {
	return &VaultInviteRepository{
		invites: make(map[uuid.UUID]*domain.VaultInvite),
	}
}

func (r *VaultInviteRepository) Create(_ context.Context, invite *domain.VaultInvite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *invite
	r.invites[invite.ID] = &stored
	return nil
}

func (r *VaultInviteRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.VaultInvite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	invite, exists := r.invites[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *invite
	return &result, nil
}

func (r *VaultInviteRepository) ListByVaultID(_ context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultInvite
	for _, inv := range r.invites {
		if inv.VaultID == vaultID {
			result = append(result, *inv)
		}
	}
	return result, nil
}

func (r *VaultInviteRepository) ListByEmail(_ context.Context, email string) ([]domain.VaultInvite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultInvite
	for _, inv := range r.invites {
		if inv.InviteeEmail == email {
			result = append(result, *inv)
		}
	}
	return result, nil
}

func (r *VaultInviteRepository) Accept(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	invite, exists := r.invites[id]
	if !exists {
		return domain.ErrNotFound
	}
	invite.Accepted = true
	return nil
}

func (r *VaultInviteRepository) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.invites[id]; !exists {
		return domain.ErrNotFound
	}
	delete(r.invites, id)
	return nil
}

func (r *VaultInviteRepository) DeleteExpired(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	now := time.Now()
	for id, invite := range r.invites {
		if invite.ExpiresAt.Before(now) && !invite.Accepted {
			delete(r.invites, id)
			count++
		}
	}
	return count, nil
}

type VaultKeyRepository struct {
	mu   sync.RWMutex
	keys map[string]*domain.VaultKey
}

func NewVaultKeyRepository() *VaultKeyRepository {
	return &VaultKeyRepository{
		keys: make(map[string]*domain.VaultKey),
	}
}

func vaultKeyKey(vaultID, userID uuid.UUID) string {
	return vaultID.String() + ":" + userID.String()
}

func (r *VaultKeyRepository) Upsert(_ context.Context, key *domain.VaultKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *key
	r.keys[vaultKeyKey(key.VaultID, key.UserID)] = &stored
	return nil
}

func (r *VaultKeyRepository) GetByVaultAndUser(_ context.Context, vaultID, userID uuid.UUID) (*domain.VaultKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, exists := r.keys[vaultKeyKey(vaultID, userID)]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *key
	return &result, nil
}

func (r *VaultKeyRepository) ListByVaultID(_ context.Context, vaultID uuid.UUID) ([]domain.VaultKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultKey
	for _, k := range r.keys {
		if k.VaultID == vaultID {
			result = append(result, *k)
		}
	}
	return result, nil
}

func (r *VaultKeyRepository) Delete(_ context.Context, vaultID, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := vaultKeyKey(vaultID, userID)
	if _, exists := r.keys[key]; !exists {
		return domain.ErrNotFound
	}
	delete(r.keys, key)
	return nil
}
