package vault_test

import (
	"context"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type encryptionTestSetup struct {
	uc             *vault.EncryptionService
	vaultUC        *vault.Service
	encryptionRepo *fake.VaultEncryptionRepository
}

func newEncryptionTestSetup() *encryptionTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	encryptionRepo := fake.NewVaultEncryptionRepository()
	vaultRepo.ResolveSummariesWith(memberRepo, keyRepo)
	vaultUC := vault.NewService(vault.Deps{Vaults: vaultRepo, Members: memberRepo, Keys: keyRepo, Invites: inviteRepo, Tx: fake.NewTransactor()})
	encryptionUC := vault.NewEncryptionService(vault.EncryptionDeps{Encryptions: encryptionRepo, Members: memberRepo})

	return &encryptionTestSetup{
		uc:             encryptionUC,
		vaultUC:        vaultUC,
		encryptionRepo: encryptionRepo,
	}
}

func TestVaultEncryptionGet_NoKey(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	info, err := s.uc.Get(context.Background(), userID, testVault.ID)
	require.NoError(t, err)
	assert.False(t, info.HasKey)
	assert.Nil(t, info.Salt)
	assert.Nil(t, info.EncryptedVEK)
}

func TestVaultEncryptionCreate_Success(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), vault.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		Salt:         []byte("test-salt-16bytes"),
		EncryptedVEK: []byte("test-encrypted-vek"),
	})
	require.NoError(t, err)

	info, err := s.uc.Get(context.Background(), userID, testVault.ID)
	require.NoError(t, err)
	assert.True(t, info.HasKey)
	assert.Equal(t, []byte("test-salt-16bytes"), info.Salt)
	assert.Equal(t, []byte("test-encrypted-vek"), info.EncryptedVEK)
}

func TestVaultEncryptionCreate_EmptyInput(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), vault.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		Salt:         nil,
		EncryptedVEK: []byte("vek"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	err = s.uc.Create(context.Background(), vault.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		Salt:         []byte("salt"),
		EncryptedVEK: nil,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestVaultEncryptionCreate_NotMember(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), vault.CreateVaultEncryptionInput{
		ActorID:      uuid.New(),
		VaultID:      testVault.ID,
		Salt:         []byte("salt"),
		EncryptedVEK: []byte("vek"),
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultEncryptionGet_NotMember(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Get(context.Background(), uuid.New(), testVault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultEncryptionCreate_Upsert(t *testing.T) {
	s := newEncryptionTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	err := s.uc.Create(context.Background(), vault.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		Salt:         []byte("salt-1"),
		EncryptedVEK: []byte("vek-1"),
	})
	require.NoError(t, err)

	err = s.uc.Create(context.Background(), vault.CreateVaultEncryptionInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		Salt:         []byte("salt-2"),
		EncryptedVEK: []byte("vek-2"),
	})
	require.NoError(t, err)

	info, err := s.uc.Get(context.Background(), userID, testVault.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("salt-2"), info.Salt)
	assert.Equal(t, []byte("vek-2"), info.EncryptedVEK)
}
