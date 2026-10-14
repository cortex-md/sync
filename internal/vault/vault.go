package vault

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/google/uuid"
)

const (
	CreationEveryone = "everyone"
	CreationAdmins   = "admins"
)

type Service struct {
	vaults         VaultRepository
	members        VaultMemberRepository
	keys           VaultKeyRepository
	users          UserRepository
	snapshots      FileSnapshotRepository
	shares         ShareBlobLister
	blobQueue      BlobQueue
	tx             Transactor
	authz          *authz.Authorizer
	creationPolicy string
	quotaManaged   bool
}

type Deps struct {
	Vaults         VaultRepository
	Members        VaultMemberRepository
	Keys           VaultKeyRepository
	Invites        VaultInviteRepository
	Users          UserRepository
	Snapshots      FileSnapshotRepository
	Shares         ShareBlobLister
	BlobQueue      BlobQueue
	Tx             Transactor
	Authz          *authz.Authorizer
	CreationPolicy string
	QuotaManaged   bool
}

func NewService(d Deps) *Service {
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	policy := d.CreationPolicy
	if policy == "" {
		policy = CreationEveryone
	}
	return &Service{
		vaults:         d.Vaults,
		members:        d.Members,
		keys:           d.Keys,
		users:          d.Users,
		snapshots:      d.Snapshots,
		shares:         d.Shares,
		blobQueue:      d.BlobQueue,
		tx:             d.Tx,
		authz:          authorizer,
		creationPolicy: policy,
		quotaManaged:   d.QuotaManaged,
	}
}

type CreateVaultInput struct {
	ID                uuid.UUID
	Name              string
	Description       string
	Icon              string
	UserID            uuid.UUID
	EncryptedVaultKey []byte
}

type VaultInfo struct {
	ID                  uuid.UUID
	Name                string
	Description         string
	Icon                string
	OwnerID             uuid.UUID
	Role                domain.VaultRole
	MemberCount         int
	HasKey              bool
	AllowedEmailDomains []string
	AllowGuests         bool
	AllowPublicLinks    bool
	StorageUsedBytes    int64
	StorageQuotaBytes   *int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func loadVaultInfo(ctx context.Context, vaults VaultRepository, vaultID, userID uuid.UUID) (*VaultInfo, error) {
	summary, err := vaults.GetSummaryForUser(ctx, vaultID, userID)
	if err != nil {
		return nil, err
	}
	info := toVaultInfo(&summary.Vault, summary.Role)
	info.MemberCount = summary.MemberCount
	info.HasKey = summary.HasKey
	return info, nil
}

func toVaultInfo(v *domain.Vault, role domain.VaultRole) *VaultInfo {
	return &VaultInfo{
		ID:                  v.ID,
		Name:                v.Name,
		Description:         v.Description,
		Icon:                v.Icon,
		OwnerID:             v.OwnerID,
		Role:                role,
		AllowedEmailDomains: v.AllowedEmailDomains,
		AllowGuests:         v.AllowGuests,
		AllowPublicLinks:    v.AllowPublicLinks,
		StorageUsedBytes:    v.StorageUsedBytes,
		StorageQuotaBytes:   v.StorageQuotaBytes,
		CreatedAt:           v.CreatedAt,
		UpdatedAt:           v.UpdatedAt,
	}
}

func (uc *Service) Create(ctx context.Context, input CreateVaultInput) (*VaultInfo, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return nil, domain.ErrInvalidInput
	}
	if err := uc.checkCreationPolicy(ctx, input.UserID); err != nil {
		return nil, err
	}

	now := time.Now()
	vault := &domain.Vault{
		ID:               input.ID,
		Name:             input.Name,
		Description:      input.Description,
		Icon:             input.Icon,
		OwnerID:          input.UserID,
		AllowGuests:      true,
		AllowPublicLinks: true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if vault.ID == uuid.Nil {
		vault.ID = uuid.New()
	}

	err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if txErr := uc.vaults.Create(ctx, vault); txErr != nil {
			return txErr
		}
		if txErr := uc.members.Add(ctx, &domain.VaultMember{
			VaultID:  vault.ID,
			UserID:   input.UserID,
			Role:     domain.VaultRoleOwner,
			JoinedAt: now,
		}); txErr != nil {
			return txErr
		}
		if len(input.EncryptedVaultKey) == 0 {
			return nil
		}
		return uc.keys.Upsert(ctx, &domain.VaultKey{
			VaultID:      vault.ID,
			UserID:       input.UserID,
			EncryptedKey: input.EncryptedVaultKey,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	})
	if errors.Is(err, domain.ErrAlreadyExists) && input.ID != uuid.Nil {
		existing, getErr := uc.vaults.GetByID(ctx, input.ID)
		if getErr != nil {
			return nil, getErr
		}
		if existing.OwnerID != input.UserID {
			return nil, domain.ErrAlreadyExists
		}
		return loadVaultInfo(ctx, uc.vaults, input.ID, input.UserID)
	}
	if err != nil {
		return nil, err
	}

	info := toVaultInfo(vault, domain.VaultRoleOwner)
	info.MemberCount = 1
	info.HasKey = len(input.EncryptedVaultKey) > 0
	return info, nil
}

func (uc *Service) checkCreationPolicy(ctx context.Context, userID uuid.UUID) error {
	if uc.creationPolicy != CreationAdmins || uc.users == nil {
		return nil
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !user.IsInstanceAdmin {
		return domain.ErrInsufficientRole
	}
	return nil
}

func (uc *Service) List(ctx context.Context, userID uuid.UUID) ([]VaultInfo, error) {
	summaries, err := uc.vaults.ListSummariesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	result := make([]VaultInfo, 0, len(summaries))
	for i := range summaries {
		info := toVaultInfo(&summaries[i].Vault, summaries[i].Role)
		info.MemberCount = summaries[i].MemberCount
		info.HasKey = summaries[i].HasKey
		result = append(result, *info)
	}
	return result, nil
}

func (uc *Service) Get(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID) (*VaultInfo, error) {
	if _, err := uc.authz.Require(ctx, vaultID, userID, domain.PermFileRead); err != nil {
		return nil, err
	}
	return loadVaultInfo(ctx, uc.vaults, vaultID, userID)
}

type UpdateVaultInput struct {
	UserID              uuid.UUID
	VaultID             uuid.UUID
	Name                string
	Description         *string
	Icon                *string
	AllowedEmailDomains *[]string
	AllowGuests         *bool
	AllowPublicLinks    *bool
	StorageQuotaBytes   **int64
}

func (uc *Service) Update(ctx context.Context, input UpdateVaultInput) (*VaultInfo, error) {
	_, err := uc.authz.Require(ctx, input.VaultID, input.UserID, domain.PermVaultUpdate)
	if err != nil {
		return nil, err
	}

	vault, err := uc.vaults.GetByID(ctx, input.VaultID)
	if err != nil {
		return nil, err
	}

	if name := strings.TrimSpace(input.Name); name != "" {
		vault.Name = name
	}
	if input.Description != nil {
		vault.Description = *input.Description
	}
	if input.Icon != nil {
		vault.Icon = *input.Icon
	}
	if input.AllowedEmailDomains != nil {
		domains, ok := normalizeDomains(*input.AllowedEmailDomains)
		if !ok {
			return nil, domain.ErrInvalidInput
		}
		vault.AllowedEmailDomains = domains
	}
	if input.AllowGuests != nil {
		vault.AllowGuests = *input.AllowGuests
	}
	if input.AllowPublicLinks != nil {
		vault.AllowPublicLinks = *input.AllowPublicLinks
	}
	if input.StorageQuotaBytes != nil {
		if !uc.quotaManaged {
			return nil, domain.ErrInvalidInput
		}
		if err := uc.requireInstanceAdmin(ctx, input.UserID); err != nil {
			return nil, err
		}
		vault.StorageQuotaBytes = *input.StorageQuotaBytes
	}
	vault.UpdatedAt = time.Now()

	if err := uc.vaults.Update(ctx, vault); err != nil {
		return nil, err
	}
	return loadVaultInfo(ctx, uc.vaults, input.VaultID, input.UserID)
}

func (uc *Service) requireInstanceAdmin(ctx context.Context, userID uuid.UUID) error {
	if uc.users == nil {
		return domain.ErrInsufficientRole
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !user.IsInstanceAdmin {
		return domain.ErrInsufficientRole
	}
	return nil
}

func normalizeDomains(domains []string) ([]string, bool) {
	result := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d), "@")))
		if d == "" {
			continue
		}
		if strings.ContainsAny(d, " @/") || !strings.Contains(d, ".") {
			return nil, false
		}
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	return result, true
}

func emailDomainAllowed(allowed []string, email string) bool {
	if len(allowed) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	emailDomain := strings.ToLower(email[at+1:])
	for _, d := range allowed {
		if emailDomain == d {
			return true
		}
	}
	return false
}

func (uc *Service) Delete(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID) error {
	if _, err := uc.authz.Require(ctx, vaultID, userID, domain.PermVaultDelete); err != nil {
		return err
	}

	return uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if uc.snapshots != nil && uc.blobQueue != nil {
			keys, err := uc.snapshots.ListBlobKeysByVault(ctx, vaultID)
			if err != nil {
				return err
			}
			if uc.shares != nil {
				shareKeys, err := uc.shares.ListBlobsByVault(ctx, vaultID)
				if err != nil {
					return err
				}
				keys = append(keys, shareKeys...)
			}
			if err := uc.blobQueue.EnqueueDeletion(ctx, keys...); err != nil {
				return err
			}
		}
		return uc.vaults.Delete(ctx, vaultID)
	})
}
