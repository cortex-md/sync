package vault_test

import (
	"context"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type keyTestSetup struct {
	uc         *vault.KeyService
	vaultUC    *vault.Service
	memberRepo *fake.VaultMemberRepository
	keyRepo    *fake.VaultKeyRepository
}

func newKeyTestSetup() *keyTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	vaultRepo.ResolveSummariesWith(memberRepo, keyRepo)
	vaultUC := vault.NewService(vault.Deps{Vaults: vaultRepo, Members: memberRepo, Keys: keyRepo, Invites: inviteRepo, Tx: fake.NewTransactor()})
	keyUC := vault.NewKeyService(vault.KeyDeps{Keys: keyRepo, Members: memberRepo})

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
	testVault := createTestVault(t, s.vaultUC, userID)

	key, err := s.uc.Upsert(context.Background(), vault.UpsertVaultKeyInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		EncryptedKey: []byte("new-encrypted-key"),
	})

	require.NoError(t, err)
	assert.Equal(t, testVault.ID, key.VaultID)
	assert.Equal(t, userID, key.UserID)
	assert.Equal(t, []byte("new-encrypted-key"), key.EncryptedKey)
}

func TestVaultKeyUpsert_EmptyKey(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Upsert(context.Background(), vault.UpsertVaultKeyInput{
		ActorID:      userID,
		VaultID:      testVault.ID,
		EncryptedKey: nil,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestVaultKeyUpsert_NotMember(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Upsert(context.Background(), vault.UpsertVaultKeyInput{
		ActorID:      uuid.New(),
		VaultID:      testVault.ID,
		EncryptedKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultKeyGet_Success(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	key, err := s.uc.Get(context.Background(), userID, testVault.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("encrypted-key-data"), key.EncryptedKey)
}

func TestVaultKeyGet_NotMember(t *testing.T) {
	s := newKeyTestSetup()
	userID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, userID)

	_, err := s.uc.Get(context.Background(), uuid.New(), testVault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultKeyListByVault_Success(t *testing.T) {
	s := newKeyTestSetup()
	ownerID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, ownerID)

	editorID := uuid.New()
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editorID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})
	s.keyRepo.Upsert(context.Background(), &domain.VaultKey{
		VaultID: testVault.ID, UserID: editorID, EncryptedKey: []byte("editor-key"),
	})

	keys, err := s.uc.ListByVault(context.Background(), ownerID, testVault.ID)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}

func TestVaultKeyListByVault_InsufficientRole(t *testing.T) {
	s := newKeyTestSetup()
	ownerID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, ownerID)

	editorID := uuid.New()
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editorID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	_, err := s.uc.ListByVault(context.Background(), editorID, testVault.ID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestVaultKeyListByVault_NotMember(t *testing.T) {
	s := newKeyTestSetup()
	ownerID := uuid.New()
	testVault := createTestVault(t, s.vaultUC, ownerID)

	_, err := s.uc.ListByVault(context.Background(), uuid.New(), testVault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}
