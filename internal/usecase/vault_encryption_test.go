package usecase_test

import (
	"context"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type encryptionTestSetup struct {
	uc             *usecase.VaultEncryptionUsecase
	vaultUC        *usecase.VaultUsecase
	encryptionRepo *fake.VaultEncryptionRepository
}

func newEncryptionTestSetup() *encryptionTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	encryptionRepo := fake.NewVaultEncryptionRepository()

	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	encryptionUC := usecase.NewVaultEncryptionUsecase(encryptionRepo, memberRepo)

	return &encryptionTestSetup{
		uc:             encryptionUC,
		vaultUC:        vaultUC,
		encryptionRepo: encryptionRepo,
	}
}

func TestVaultEncryptionGet_NoKey(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	info, err := s.uc.Get(context.Background(), userID, vault.ID)
	require.NoError(t, err)
	assert.False(t, info.HasKey)
	assert.Nil(t, info.Salt)
	assert.Nil(t, info.EncryptedVEK)
}

func TestVaultEncryptionCreate_Success(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), usecase.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		Salt:         []byte("test-salt-16bytes"),
		EncryptedVEK: []byte("test-encrypted-vek"),
	})
	require.NoError(t, err)

	info, err := s.uc.Get(context.Background(), userID, vault.ID)
	require.NoError(t, err)
	assert.True(t, info.HasKey)
	assert.Equal(t, []byte("test-salt-16bytes"), info.Salt)
	assert.Equal(t, []byte("test-encrypted-vek"), info.EncryptedVEK)
}

func TestVaultEncryptionCreate_EmptyInput(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), usecase.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		Salt:         nil,
		EncryptedVEK: []byte("vek"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	err = s.uc.Create(context.Background(), usecase.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		Salt:         []byte("salt"),
		EncryptedVEK: nil,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestVaultEncryptionCreate_NotMember(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), usecase.CreateVaultEncryptionInput{
		ActorID:      uuid.New(),
		VaultID:      vault.ID,
		Salt:         []byte("salt"),
		EncryptedVEK: []byte("vek"),
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultEncryptionGet_NotMember(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Get(context.Background(), uuid.New(), vault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultEncryptionCreate_Upsert(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), usecase.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		Salt:         []byte("salt-1"),
		EncryptedVEK: []byte("vek-1"),
	})
	require.NoError(t, err)

	err = s.uc.Create(context.Background(), usecase.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		Salt:         []byte("salt-2"),
		EncryptedVEK: []byte("vek-2"),
	})
	require.NoError(t, err)

	info, err := s.uc.Get(context.Background(), userID, vault.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("salt-2"), info.Salt)
	assert.Equal(t, []byte("vek-2"), info.EncryptedVEK)
}
