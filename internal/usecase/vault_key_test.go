package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type keyTestSetup struct {
	uc         *usecase.VaultKeyUsecase
	vaultUC    *usecase.VaultUsecase
	memberRepo *fake.VaultMemberRepository
	keyRepo    *fake.VaultKeyRepository
}

func newKeyTestSetup() *keyTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()

	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	keyUC := usecase.NewVaultKeyUsecase(keyRepo, memberRepo)

	return &keyTestSetup{
		uc:         keyUC,
		vaultUC:    vaultUC,
		memberRepo: memberRepo,
		keyRepo:    keyRepo,
	}
}

func TestVaultKeyUpsert_Success(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	key, err := s.uc.Upsert(context.Background(), usecase.UpsertVaultKeyInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		EncryptedKey: []byte("new-encrypted-key"),
	})

	require.NoError(t, err)
	assert.Equal(t, vault.ID, key.VaultID)
	assert.Equal(t, userID, key.UserID)
	assert.Equal(t, []byte("new-encrypted-key"), key.EncryptedKey)
}

func TestVaultKeyUpsert_EmptyKey(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Upsert(context.Background(), usecase.UpsertVaultKeyInput{
		ActorID:      userID,
		VaultID:      vault.ID,
		EncryptedKey: nil,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestVaultKeyUpsert_NotMember(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Upsert(context.Background(), usecase.UpsertVaultKeyInput{
		ActorID:      uuid.New(),
		VaultID:      vault.ID,
		EncryptedKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultKeyGet_Success(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	key, err := s.uc.Get(context.Background(), userID, vault.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("encrypted-key-data"), key.EncryptedKey)
}

func TestVaultKeyGet_NotMember(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	vault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Get(context.Background(), uuid.New(), vault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultKeyListByVault_Success(t *testing.T) {
	s := newKeyTestSetup()
	ownerID := uuid.New()
	vault := createTestVault(t, s.vaultUC, ownerID)

	editorID := uuid.New()
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vault.ID, UserID: editorID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})
	s.keyRepo.Upsert(context.Background(), &domain.VaultKey{
		VaultID: vault.ID, UserID: editorID, EncryptedKey: []byte("editor-key"),
	})

	keys, err := s.uc.ListByVault(context.Background(), ownerID, vault.ID)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}

func TestVaultKeyListByVault_InsufficientRole(t *testing.T) {
	s := newKeyTestSetup()
	ownerID := uuid.New()
	vault := createTestVault(t, s.vaultUC, ownerID)

	editorID := uuid.New()
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vault.ID, UserID: editorID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	_, err := s.uc.ListByVault(context.Background(), editorID, vault.ID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestVaultKeyListByVault_NotMember(t *testing.T) {
	s := newKeyTestSetup()
	ownerID := uuid.New()
	vault := createTestVault(t, s.vaultUC, ownerID)

	_, err := s.uc.ListByVault(context.Background(), uuid.New(), vault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}
