package usecase

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type VaultKeyUsecase struct {
	keys    port.VaultKeyRepository
	members port.VaultMemberRepository
}

func NewVaultKeyUsecase(
	keys port.VaultKeyRepository,
	members port.VaultMemberRepository,
) *VaultKeyUsecase {
	return &VaultKeyUsecase{
		keys:    keys,
		members: members,
	}
}

type VaultKeyInfo struct {
	VaultID      uuid.UUID
	UserID       uuid.UUID
	EncryptedKey []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type UpsertVaultKeyInput struct {
	ActorID      uuid.UUID
	VaultID      uuid.UUID
	EncryptedKey []byte
}

func (uc *VaultKeyUsecase) Upsert(ctx context.Context, input UpsertVaultKeyInput) (*VaultKeyInfo, error) {
	if len(input.EncryptedKey) == 0 {
		return nil, domain.ErrInvalidInput
	}

	_, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.ActorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	now := time.Now()
	key := &domain.VaultKey{
		VaultID:      input.VaultID,
		UserID:       input.ActorID,
		EncryptedKey: input.EncryptedKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := uc.keys.Upsert(ctx, key); err != nil {
		return nil, err
	}

	return &VaultKeyInfo{
		VaultID:      key.VaultID,
		UserID:       key.UserID,
		EncryptedKey: key.EncryptedKey,
		CreatedAt:    key.CreatedAt,
		UpdatedAt:    key.UpdatedAt,
	}, nil
}

func (uc *VaultKeyUsecase) Get(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) (*VaultKeyInfo, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	key, err := uc.keys.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		return nil, err
	}

	return &VaultKeyInfo{
		VaultID:      key.VaultID,
		UserID:       key.UserID,
		EncryptedKey: key.EncryptedKey,
		CreatedAt:    key.CreatedAt,
		UpdatedAt:    key.UpdatedAt,
	}, nil
}

func (uc *VaultKeyUsecase) ListByVault(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]VaultKeyInfo, error) {
	actor, err := uc.members.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !actor.Role.CanManageMembers() {
		return nil, domain.ErrInsufficientRole
	}

	keys, err := uc.keys.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	result := make([]VaultKeyInfo, 0, len(keys))
	for _, k := range keys {
		result = append(result, VaultKeyInfo{
			VaultID:      k.VaultID,
			UserID:       k.UserID,
			EncryptedKey: k.EncryptedKey,
			CreatedAt:    k.CreatedAt,
			UpdatedAt:    k.UpdatedAt,
		})
	}

	return result, nil
}
