package fake

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type VaultRepository struct {
	mu      sync.RWMutex
	vaults  map[uuid.UUID]*domain.Vault
	members *VaultMemberRepository
	keys    *VaultKeyRepository
}

func NewVaultRepository() *VaultRepository {
	return &VaultRepository{
		vaults: make(map[uuid.UUID]*domain.Vault),
	}
}

func (r *VaultRepository) Create(_ context.Context, vault *domain.Vault) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.vaults[vault.ID]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *vault
	r.vaults[vault.ID] = &stored
	return nil
}

func (r *VaultRepository) ResolveSummariesWith(members *VaultMemberRepository, keys *VaultKeyRepository) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members = members
	r.keys = keys
}

func (r *VaultRepository) GetSummaryForUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultSummary, error) {
	summaries, err := r.ListSummariesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range summaries {
		if summaries[i].Vault.ID == vaultID {
			return &summaries[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *VaultRepository) ListSummariesForUser(ctx context.Context, userID uuid.UUID) ([]domain.VaultSummary, error) {
	r.mu.RLock()
	members, keys := r.members, r.keys
	r.mu.RUnlock()
	if members == nil {
		return nil, nil
	}
	memberships, err := members.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var result []domain.VaultSummary
	for _, m := range memberships {
		vault, err := r.GetByID(ctx, m.VaultID)
		if err != nil {
			continue
		}
		all, _ := members.ListByVaultID(ctx, m.VaultID)
		hasKey := false
		if keys != nil {
			_, keyErr := keys.GetByVaultAndUser(ctx, m.VaultID, userID)
			hasKey = keyErr == nil
		}
		result = append(result, domain.VaultSummary{Vault: *vault, Role: m.Role, MemberCount: len(all), HasKey: hasKey})
	}
	return result, nil
}

func (r *VaultRepository) AddStorageWithinQuota(_ context.Context, id uuid.UUID, delta int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vaults[id]
	if !ok {
		return domain.ErrNotFound
	}
	if v.StorageQuotaBytes != nil && v.StorageUsedBytes+delta > *v.StorageQuotaBytes {
		return domain.ErrQuotaExceeded
	}
	v.StorageUsedBytes = max(v.StorageUsedBytes+delta, 0)
	return nil
}

func (r *VaultRepository) AddStorage(_ context.Context, id uuid.UUID, delta int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.vaults[id]; ok {
		v.StorageUsedBytes = max(v.StorageUsedBytes+delta, 0)
	}
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
	groups  map[uuid.UUID]*domain.VaultGroup
	rules   map[uuid.UUID]map[string]*domain.PathRule
	files   *FileLatestRepository
}

func NewVaultMemberRepository() *VaultMemberRepository {
	return &VaultMemberRepository{
		members: make(map[string]*domain.VaultMember),
		groups:  make(map[uuid.UUID]*domain.VaultGroup),
		rules:   make(map[uuid.UUID]map[string]*domain.PathRule),
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
	vaults  *VaultRepository
	users   *UserRepository
}

func NewVaultInviteRepository() *VaultInviteRepository {
	return &VaultInviteRepository{
		invites: make(map[uuid.UUID]*domain.VaultInvite),
	}
}

func (r *VaultInviteRepository) Create(_ context.Context, invite *domain.VaultInvite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.invites[invite.ID]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *invite
	r.invites[invite.ID] = &stored
	return nil
}

func (r *VaultInviteRepository) RotateToken(_ context.Context, id uuid.UUID, tokenHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	invite, ok := r.invites[id]
	if !ok || invite.Accepted {
		return domain.ErrNotFound
	}
	invite.TokenHash = tokenHash
	return nil
}

func (r *VaultInviteRepository) PendingExists(_ context.Context, vaultID uuid.UUID, email string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inv := range r.invites {
		if inv.VaultID == vaultID && !inv.Accepted && time.Now().Before(inv.ExpiresAt) && strings.EqualFold(inv.InviteeEmail, email) {
			return true, nil
		}
	}
	return false, nil
}

func (r *VaultInviteRepository) ResolveDetailsWith(vaults *VaultRepository, users *UserRepository) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vaults = vaults
	r.users = users
}

func (r *VaultInviteRepository) ListPendingForEmail(ctx context.Context, email string) ([]domain.InviteDetails, error) {
	r.mu.RLock()
	var pending []domain.VaultInvite
	for _, inv := range r.invites {
		if strings.EqualFold(inv.InviteeEmail, email) && !inv.Accepted && time.Now().Before(inv.ExpiresAt) {
			pending = append(pending, *inv)
		}
	}
	vaults, users := r.vaults, r.users
	r.mu.RUnlock()

	details := make([]domain.InviteDetails, 0, len(pending))
	for _, inv := range pending {
		d := domain.InviteDetails{VaultInvite: inv}
		if vaults != nil {
			if v, err := vaults.GetByID(ctx, inv.VaultID); err == nil {
				d.VaultName = v.Name
			}
		}
		if users != nil {
			if u, err := users.GetByID(ctx, inv.InviterID); err == nil {
				d.InviterName, d.InviterEmail = u.DisplayName, u.Email
			}
		}
		details = append(details, d)
	}
	return details, nil
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
	if invite.Accepted {
		return domain.ErrAlreadyExists
	}
	invite.Accepted = true
	return nil
}

func (r *VaultInviteRepository) GetByTokenHash(_ context.Context, tokenHash string) (*domain.VaultInvite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inv := range r.invites {
		if tokenHash != "" && inv.TokenHash == tokenHash {
			result := *inv
			return &result, nil
		}
	}
	return nil, domain.ErrNotFound
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

func (r *VaultKeyRepository) DeleteForUser(_ context.Context, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, k := range r.keys {
		if k.UserID == userID {
			delete(r.keys, key)
		}
	}
	return nil
}

func (r *VaultKeyRepository) HasAny(_ context.Context, vaultID uuid.UUID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.keys {
		if k.VaultID == vaultID {
			return true, nil
		}
	}
	return false, nil
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

type VaultEncryptionRepository struct {
	mu          sync.RWMutex
	encryptions map[uuid.UUID]*domain.VaultEncryption
}

func NewVaultEncryptionRepository() *VaultEncryptionRepository {
	return &VaultEncryptionRepository{
		encryptions: make(map[uuid.UUID]*domain.VaultEncryption),
	}
}

func (r *VaultEncryptionRepository) Upsert(_ context.Context, enc *domain.VaultEncryption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *enc
	r.encryptions[enc.VaultID] = &stored
	return nil
}

func (r *VaultEncryptionRepository) GetByVaultID(_ context.Context, vaultID uuid.UUID) (*domain.VaultEncryption, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	enc, exists := r.encryptions[vaultID]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *enc
	return &result, nil
}

func (r *VaultInviteRepository) ListPendingByVault(_ context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultInvite
	for _, inv := range r.invites {
		if inv.VaultID == vaultID && !inv.Accepted {
			result = append(result, *inv)
		}
	}
	return result, nil
}
