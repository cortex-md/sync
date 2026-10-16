package authz

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

const (
	cacheTTL        = 30 * time.Second
	maxCacheEntries = 100_000
)

type Members interface {
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error)
	ListRuleAccess(ctx context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error)
}

type Access struct {
	Member domain.VaultMember
	Scope  domain.Scope
}

type cacheEntry struct {
	access    Access
	expiresAt time.Time
}

type Authorizer struct {
	guestsAllowed func(ctx context.Context, vaultID uuid.UUID) (bool, error)
	members       Members
	cached        bool
	mu            sync.RWMutex
	entries       map[uuid.UUID]map[uuid.UUID]cacheEntry
	size          int
	generation    uint64
}

func New(members Members) *Authorizer {
	return &Authorizer{members: members}
}

func NewCached(members Members) *Authorizer {
	return &Authorizer{members: members, cached: true, entries: make(map[uuid.UUID]map[uuid.UUID]cacheEntry)}
}

func (a *Authorizer) Invalidate(vaultID, userID uuid.UUID) {
	if !a.cached {
		return
	}
	a.mu.Lock()
	a.generation++
	if users, ok := a.entries[vaultID]; ok {
		if _, ok := users[userID]; ok {
			delete(users, userID)
			a.size--
		}
		if len(users) == 0 {
			delete(a.entries, vaultID)
		}
	}
	a.mu.Unlock()
}

func (a *Authorizer) InvalidateAll() {
	a.mu.Lock()
	a.generation++
	a.entries = make(map[uuid.UUID]map[uuid.UUID]cacheEntry)
	a.size = 0
	a.mu.Unlock()
}

func (a *Authorizer) InvalidateVault(vaultID uuid.UUID) {
	if !a.cached {
		return
	}
	a.mu.Lock()
	a.generation++
	a.size -= len(a.entries[vaultID])
	delete(a.entries, vaultID)
	a.mu.Unlock()
}

func (a *Authorizer) Access(ctx context.Context, vaultID, userID uuid.UUID, permission domain.Permission) (Access, error) {
	access, err := a.load(ctx, vaultID, userID)
	if err != nil {
		return Access{}, err
	}
	if !access.Member.Role.Can(permission) {
		return Access{}, domain.ErrInsufficientRole
	}
	return access, nil
}

func (a *Authorizer) Require(ctx context.Context, vaultID, userID uuid.UUID, permission domain.Permission) (*domain.VaultMember, error) {
	access, err := a.Access(ctx, vaultID, userID, permission)
	if err != nil {
		return nil, err
	}
	member := access.Member
	return &member, nil
}

func (a *Authorizer) RequirePath(ctx context.Context, vaultID, userID uuid.UUID, path string, permission domain.Permission) (*domain.VaultMember, error) {
	access, err := a.Access(ctx, vaultID, userID, permission&^(domain.PermFileRead|domain.PermFileWrite))
	if err != nil {
		return nil, err
	}
	allowed := access.Scope.CanRead(path)
	if permission&domain.PermFileWrite != 0 {
		allowed = access.Scope.CanWrite(path)
	}
	if !allowed {
		return nil, domain.ErrInsufficientRole
	}
	member := access.Member
	return &member, nil
}

func (a *Authorizer) load(ctx context.Context, vaultID, userID uuid.UUID) (Access, error) {
	var generation uint64
	if a.cached {
		a.mu.RLock()
		entry, ok := a.entries[vaultID][userID]
		generation = a.generation
		a.mu.RUnlock()
		if ok && time.Now().Before(entry.expiresAt) {
			return entry.access, nil
		}
	}

	member, err := a.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return Access{}, domain.ErrVaultAccessDenied
		}
		return Access{}, err
	}

	access := Access{Member: *member, Scope: domain.FullScope()}
	if !domain.HasFullAccess(member.Role) {
		rules, err := a.members.ListRuleAccess(ctx, vaultID, userID)
		if err != nil {
			return Access{}, err
		}
		if member.Role == domain.VaultRoleGuest && a.guestsAllowed != nil {
			allowed, err := a.guestsAllowed(ctx, vaultID)
			if err != nil {
				return Access{}, err
			}
			if !allowed {
				rules = nil
			}
		}
		access.Scope = domain.CompileScope(member.Role, rules)
	}

	if a.cached {
		a.store(vaultID, userID, access, generation)
	}
	return access, nil
}

func (a *Authorizer) store(vaultID, userID uuid.UUID, access Access, generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.generation != generation {
		return
	}
	if a.size >= maxCacheEntries {
		a.entries = make(map[uuid.UUID]map[uuid.UUID]cacheEntry)
		a.size = 0
	}
	users, ok := a.entries[vaultID]
	if !ok {
		users = make(map[uuid.UUID]cacheEntry)
		a.entries[vaultID] = users
	}
	if _, exists := users[userID]; !exists {
		a.size++
	}
	users[userID] = cacheEntry{access: access, expiresAt: time.Now().Add(cacheTTL)}
}

func (a *Authorizer) WithGuestPolicy(allowed func(ctx context.Context, vaultID uuid.UUID) (bool, error)) *Authorizer {
	a.guestsAllowed = allowed
	return a
}
