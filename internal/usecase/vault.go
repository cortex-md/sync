package usecase

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type VaultUsecase struct {
	vaults  port.VaultRepository
	members port.VaultMemberRepository
	keys    port.VaultKeyRepository
	invites port.VaultInviteRepository
	tx      port.Transactor
}

func NewVaultUsecase(
	vaults port.VaultRepository,
	members port.VaultMemberRepository,
	keys port.VaultKeyRepository,
	invites port.VaultInviteRepository,
	tx port.Transactor,
) *VaultUsecase {
	return &VaultUsecase{
		vaults:  vaults,
		members: members,
		keys:    keys,
		invites: invites,
		tx:      tx,
	}
}

type CreateVaultInput struct {
	Name              string
	Description       string
	UserID            uuid.UUID
	EncryptedVaultKey []byte
}

type VaultInfo struct {
	ID          uuid.UUID
	Name        string
	Description string
	OwnerID     uuid.UUID
	Role        domain.VaultRole
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (uc *VaultUsecase) Create(ctx context.Context, input CreateVaultInput) (*VaultInfo, error) {
	if input.Name == "" {
		return nil, domain.ErrInvalidInput
	}

	if len(input.EncryptedVaultKey) == 0 {
		return nil, domain.ErrInvalidInput
	}

	now := time.Now()
	vault := &domain.Vault{
		ID:          uuid.New(),
		Name:        input.Name,
		Description: input.Description,
		OwnerID:     input.UserID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if txErr := uc.vaults.Create(ctx, vault); txErr != nil {
			return txErr
		}

		member := &domain.VaultMember{
			VaultID:  vault.ID,
			UserID:   input.UserID,
			Role:     domain.VaultRoleOwner,
			JoinedAt: now,
		}

		if txErr := uc.members.Add(ctx, member); txErr != nil {
			return txErr
		}

		vaultKey := &domain.VaultKey{
			VaultID:      vault.ID,
			UserID:       input.UserID,
			EncryptedKey: input.EncryptedVaultKey,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		return uc.keys.Upsert(ctx, vaultKey)
	}); err != nil {
		return nil, err
	}

	return &VaultInfo{
		ID:          vault.ID,
		Name:        vault.Name,
		Description: vault.Description,
		OwnerID:     vault.OwnerID,
		Role:        domain.VaultRoleOwner,
		CreatedAt:   vault.CreatedAt,
		UpdatedAt:   vault.UpdatedAt,
	}, nil
}

func (uc *VaultUsecase) List(ctx context.Context, userID uuid.UUID) ([]VaultInfo, error) {
	memberships, err := uc.members.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	result := make([]VaultInfo, 0, len(memberships))
	for _, m := range memberships {
		vault, err := uc.vaults.GetByID(ctx, m.VaultID)
		if err != nil {
			continue
		}
		result = append(result, VaultInfo{
			ID:          vault.ID,
			Name:        vault.Name,
			Description: vault.Description,
			OwnerID:     vault.OwnerID,
			Role:        m.Role,
			CreatedAt:   vault.CreatedAt,
			UpdatedAt:   vault.UpdatedAt,
		})
	}

	return result, nil
}

func (uc *VaultUsecase) Get(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID) (*VaultInfo, error) {
	member, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	vault, err := uc.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	return &VaultInfo{
		ID:          vault.ID,
		Name:        vault.Name,
		Description: vault.Description,
		OwnerID:     vault.OwnerID,
		Role:        member.Role,
		CreatedAt:   vault.CreatedAt,
		UpdatedAt:   vault.UpdatedAt,
	}, nil
}

type UpdateVaultInput struct {
	UserID      uuid.UUID
	VaultID     uuid.UUID
	Name        string
	Description *string
}

func (uc *VaultUsecase) Update(ctx context.Context, input UpdateVaultInput) (*VaultInfo, error) {
	member, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !member.Role.CanManageMembers() {
		return nil, domain.ErrInsufficientRole
	}

	vault, err := uc.vaults.GetByID(ctx, input.VaultID)
	if err != nil {
		return nil, err
	}

	if input.Name != "" {
		vault.Name = input.Name
	}
	if input.Description != nil {
		vault.Description = *input.Description
	}
	vault.UpdatedAt = time.Now()

	if err := uc.vaults.Update(ctx, vault); err != nil {
		return nil, err
	}

	return &VaultInfo{
		ID:          vault.ID,
		Name:        vault.Name,
		Description: vault.Description,
		OwnerID:     vault.OwnerID,
		Role:        member.Role,
		CreatedAt:   vault.CreatedAt,
		UpdatedAt:   vault.UpdatedAt,
	}, nil
}

func (uc *VaultUsecase) Delete(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID) error {
	member, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrVaultAccessDenied
		}
		return err
	}

	if !member.Role.CanDelete() {
		return domain.ErrInsufficientRole
	}

	return uc.vaults.Delete(ctx, vaultID)
}
