package vault

import (
	"context"
	"errors"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/google/uuid"
)

type EncryptionService struct {
	encryptions VaultEncryptionRepository
	keys        VaultKeyRepository
	authz       *authz.Authorizer
}

type EncryptionDeps struct {
	Encryptions VaultEncryptionRepository
	Keys        VaultKeyRepository
	Members     VaultMemberRepository
	Authz       *authz.Authorizer
}

func NewEncryptionService(d EncryptionDeps) *EncryptionService {
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &EncryptionService{
		encryptions: d.Encryptions,
		keys:        d.Keys,
		authz:       authorizer,
	}
}

type VaultEncryptionInfo struct {
	HasKey        bool
	HasMemberKeys bool
	Salt          []byte
	EncryptedVEK  []byte
}

type CreateVaultEncryptionInput struct {
	ActorID      uuid.UUID
	VaultID      uuid.UUID
	Salt         []byte
	EncryptedVEK []byte
}

func (uc *EncryptionService) Get(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) (*VaultEncryptionInfo, error) {
	if _, err := uc.authz.Require(ctx, vaultID, actorID, domain.PermFileRead); err != nil {
		return nil, err
	}

	hasMemberKeys := false
	if uc.keys != nil {
		exists, err := uc.keys.HasAny(ctx, vaultID)
		if err != nil {
			return nil, err
		}
		hasMemberKeys = exists
	}

	enc, err := uc.encryptions.GetByVaultID(ctx, vaultID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &VaultEncryptionInfo{HasKey: false, HasMemberKeys: hasMemberKeys}, nil
		}
		return nil, err
	}

	return &VaultEncryptionInfo{
		HasKey:        true,
		HasMemberKeys: hasMemberKeys,
		Salt:          enc.Salt,
		EncryptedVEK:  enc.EncryptedVEK,
	}, nil
}

func (uc *EncryptionService) Create(ctx context.Context, input CreateVaultEncryptionInput) error {
	if len(input.Salt) == 0 || len(input.EncryptedVEK) == 0 {
		return domain.ErrInvalidInput
	}

	if _, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermKeyGrant); err != nil {
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
