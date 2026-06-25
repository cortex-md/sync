package usecase

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type VaultEncryptionUsecase struct {
	encryptions port.VaultEncryptionRepository
	members     port.VaultMemberRepository
}

func NewVaultEncryptionUsecase(
	encryptions port.VaultEncryptionRepository,
	members port.VaultMemberRepository,
) *VaultEncryptionUsecase {
	return &VaultEncryptionUsecase{
		encryptions: encryptions,
		members:     members,
	}
}

type VaultEncryptionInfo struct {
	HasKey       bool
	Salt         []byte
	EncryptedVEK []byte
}

type CreateVaultEncryptionInput struct {
	ActorID      uuid.UUID
	VaultID      uuid.UUID
	Salt         []byte
	EncryptedVEK []byte
}

func (uc *VaultEncryptionUsecase) Get(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) (*VaultEncryptionInfo, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	enc, err := uc.encryptions.GetByVaultID(ctx, vaultID)
	if err != nil {
		if err == domain.ErrNotFound {
			return &VaultEncryptionInfo{HasKey: false}, nil
		}
		return nil, err
	}

	return &VaultEncryptionInfo{
		HasKey:       true,
		Salt:         enc.Salt,
		EncryptedVEK: enc.EncryptedVEK,
	}, nil
}

func (uc *VaultEncryptionUsecase) Create(ctx context.Context, input CreateVaultEncryptionInput) error {
	if len(input.Salt) == 0 || len(input.EncryptedVEK) == 0 {
		return domain.ErrInvalidInput
	}

	_, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.ActorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrVaultAccessDenied
		}
		return err
	}

	enc := &domain.VaultEncryption{
		VaultID:      input.VaultID,
		Salt:         input.Salt,
		EncryptedVEK: input.EncryptedVEK,
		CreatedAt:    time.Now(),
	}

	return uc.encryptions.Upsert(ctx, enc)
}
