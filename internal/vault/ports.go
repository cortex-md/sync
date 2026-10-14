package vault

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type BlobQueue interface {
	EnqueueDeletion(ctx context.Context, keys ...string) error
}

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error)
	UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error
}

type VaultRepository interface {
	Create(ctx context.Context, vault *domain.Vault) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
	ListSummariesForUser(ctx context.Context, userID uuid.UUID) ([]domain.VaultSummary, error)
	GetSummaryForUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultSummary, error)
	Update(ctx context.Context, vault *domain.Vault) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type VaultMemberRepository interface {
	Add(ctx context.Context, member *domain.VaultMember) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error)
	UpdateRole(ctx context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error
	Remove(ctx context.Context, vaultID, userID uuid.UUID) error
	ListRuleAccess(ctx context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error)
}

type AccessRepository interface {
	ListGroups(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultGroup, error)
	GetGroup(ctx context.Context, vaultID, groupID uuid.UUID) (*domain.VaultGroup, error)
	CreateGroup(ctx context.Context, group *domain.VaultGroup) error
	RenameGroup(ctx context.Context, vaultID, groupID uuid.UUID, name string) error
	SetGroupMembers(ctx context.Context, vaultID, groupID uuid.UUID, memberIDs []uuid.UUID) error
	DeleteGroup(ctx context.Context, vaultID, groupID uuid.UUID) error
	ListRules(ctx context.Context, vaultID uuid.UUID) ([]domain.PathRule, error)
	PutRule(ctx context.Context, rule *domain.PathRule) error
	UpsertUserEntry(ctx context.Context, vaultID uuid.UUID, path string, userID uuid.UUID, level domain.AccessLevel, updatedBy uuid.UUID) error
	RemovePrincipal(ctx context.Context, vaultID uuid.UUID, principalType domain.PrincipalType, principalID uuid.UUID) error
	RulesCovering(ctx context.Context, vaultID uuid.UUID, path string) ([]string, error)
	RuleExists(ctx context.Context, vaultID uuid.UUID, path string) (bool, error)
	LiveFileExistsUnder(ctx context.Context, vaultID uuid.UUID, prefix, exclude string) (bool, error)
	CopyRule(ctx context.Context, vaultID uuid.UUID, from, to string) error
	MoveRule(ctx context.Context, vaultID uuid.UUID, from, to string) error
	DeleteRule(ctx context.Context, vaultID uuid.UUID, path string) (bool, error)
}

type VaultInviteRepository interface {
	Create(ctx context.Context, invite *domain.VaultInvite) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.VaultInvite, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.VaultInvite, error)
	RotateToken(ctx context.Context, id uuid.UUID, tokenHash string) error
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error)
	ListPendingForEmail(ctx context.Context, email string) ([]domain.InviteDetails, error)
	PendingExists(ctx context.Context, vaultID uuid.UUID, email string) (bool, error)
	Accept(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type VaultKeyRepository interface {
	Upsert(ctx context.Context, key *domain.VaultKey) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultKey, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultKey, error)
	Delete(ctx context.Context, vaultID, userID uuid.UUID) error
	HasAny(ctx context.Context, vaultID uuid.UUID) (bool, error)
}

type VaultEncryptionRepository interface {
	Upsert(ctx context.Context, enc *domain.VaultEncryption) error
	GetByVaultID(ctx context.Context, vaultID uuid.UUID) (*domain.VaultEncryption, error)
}

type FileSnapshotRepository interface {
	ListBlobKeysByVault(ctx context.Context, vaultID uuid.UUID) ([]string, error)
}

type ShareBlobLister interface {
	ListBlobsByVault(ctx context.Context, vaultID uuid.UUID) ([]string, error)
}

type EmailOutboxRepository interface {
	Enqueue(ctx context.Context, email *domain.OutboxEmail) error
}
