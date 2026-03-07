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

func newTestVaultUsecase() (*usecase.VaultUsecase, *fake.VaultRepository, *fake.VaultMemberRepository, *fake.VaultKeyRepository, *fake.VaultInviteRepository) {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	uc := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	return uc, vaultRepo, memberRepo, keyRepo, inviteRepo
}

func createTestVault(t *testing.T, uc *usecase.VaultUsecase, userID uuid.UUID) *usecase.VaultInfo {
	t.Helper()
	vault, err := uc.Create(context.Background(), usecase.CreateVaultInput{
		Name:              "Test Vault",
		Description:       "A test vault",
		UserID:            userID,
		EncryptedVaultKey: []byte("encrypted-key-data"),
	})
	require.NoError(t, err)
	return vault
}

func TestVaultCreate_Success(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	userID := uuid.New()

	vault, err := uc.Create(context.Background(), usecase.CreateVaultInput{
		Name:              "My Vault",
		Description:       "Personal notes",
		UserID:            userID,
		EncryptedVaultKey: []byte("encrypted-key"),
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, vault.ID)
	assert.Equal(t, "My Vault", vault.Name)
	assert.Equal(t, "Personal notes", vault.Description)
	assert.Equal(t, userID, vault.OwnerID)
	assert.Equal(t, domain.VaultRoleOwner, vault.Role)
}

func TestVaultCreate_AutoAddsMemberAndKey(t *testing.T) {
	uc, _, memberRepo, keyRepo, _ := newTestVaultUsecase()
	userID := uuid.New()

	vault, err := uc.Create(context.Background(), usecase.CreateVaultInput{
		Name:              "My Vault",
		UserID:            userID,
		EncryptedVaultKey: []byte("encrypted-key"),
	})
	require.NoError(t, err)

	member, err := memberRepo.GetByVaultAndUser(context.Background(), vault.ID, userID)
	require.NoError(t, err)
	assert.Equal(t, domain.VaultRoleOwner, member.Role)

	key, err := keyRepo.GetByVaultAndUser(context.Background(), vault.ID, userID)
	require.NoError(t, err)
	assert.Equal(t, []byte("encrypted-key"), key.EncryptedKey)
}

func TestVaultCreate_EmptyName(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()

	_, err := uc.Create(context.Background(), usecase.CreateVaultInput{
		Name:              "",
		UserID:            uuid.New(),
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestVaultCreate_EmptyKey(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()

	_, err := uc.Create(context.Background(), usecase.CreateVaultInput{
		Name:              "Vault",
		UserID:            uuid.New(),
		EncryptedVaultKey: nil,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestVaultList_Success(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	userID := uuid.New()

	createTestVault(t, uc, userID)
	createTestVault(t, uc, userID)

	vaults, err := uc.List(context.Background(), userID)
	require.NoError(t, err)
	assert.Len(t, vaults, 2)
	for _, v := range vaults {
		assert.Equal(t, domain.VaultRoleOwner, v.Role)
	}
}

func TestVaultList_Empty(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()

	vaults, err := uc.List(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Empty(t, vaults)
}

func TestVaultList_OnlyUserVaults(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	user1 := uuid.New()
	user2 := uuid.New()

	createTestVault(t, uc, user1)
	createTestVault(t, uc, user2)

	vaults, err := uc.List(context.Background(), user1)
	require.NoError(t, err)
	assert.Len(t, vaults, 1)
}

func TestVaultGet_Success(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	userID := uuid.New()
	created := createTestVault(t, uc, userID)

	vault, err := uc.Get(context.Background(), userID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, vault.ID)
	assert.Equal(t, "Test Vault", vault.Name)
	assert.Equal(t, domain.VaultRoleOwner, vault.Role)
}

func TestVaultGet_NotMember(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	ownerID := uuid.New()
	created := createTestVault(t, uc, ownerID)

	_, err := uc.Get(context.Background(), uuid.New(), created.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultUpdate_Success(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	userID := uuid.New()
	created := createTestVault(t, uc, userID)

	desc := "Updated description"
	vault, err := uc.Update(context.Background(), usecase.UpdateVaultInput{
		UserID:      userID,
		VaultID:     created.ID,
		Name:        "Updated Vault",
		Description: &desc,
	})

	require.NoError(t, err)
	assert.Equal(t, "Updated Vault", vault.Name)
	assert.Equal(t, "Updated description", vault.Description)
}

func TestVaultUpdate_PartialUpdate(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	userID := uuid.New()
	created := createTestVault(t, uc, userID)

	vault, err := uc.Update(context.Background(), usecase.UpdateVaultInput{
		UserID:  userID,
		VaultID: created.ID,
		Name:    "New Name Only",
	})

	require.NoError(t, err)
	assert.Equal(t, "New Name Only", vault.Name)
	assert.Equal(t, "A test vault", vault.Description)
}

func TestVaultUpdate_NotMember(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	ownerID := uuid.New()
	created := createTestVault(t, uc, ownerID)

	_, err := uc.Update(context.Background(), usecase.UpdateVaultInput{
		UserID:  uuid.New(),
		VaultID: created.ID,
		Name:    "Hijacked",
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestVaultUpdate_InsufficientRole(t *testing.T) {
	uc, _, memberRepo, _, _ := newTestVaultUsecase()
	ownerID := uuid.New()
	created := createTestVault(t, uc, ownerID)

	viewerID := uuid.New()
	memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: created.ID,
		UserID:  viewerID,
		Role:    domain.VaultRoleViewer,
	})

	_, err := uc.Update(context.Background(), usecase.UpdateVaultInput{
		UserID:  viewerID,
		VaultID: created.ID,
		Name:    "Nope",
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestVaultDelete_Success(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	userID := uuid.New()
	created := createTestVault(t, uc, userID)

	err := uc.Delete(context.Background(), userID, created.ID)
	require.NoError(t, err)

	_, err = uc.Get(context.Background(), userID, created.ID)
	assert.Error(t, err)
}

func TestVaultDelete_NotOwner(t *testing.T) {
	uc, _, memberRepo, _, _ := newTestVaultUsecase()
	ownerID := uuid.New()
	created := createTestVault(t, uc, ownerID)

	adminID := uuid.New()
	memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: created.ID,
		UserID:  adminID,
		Role:    domain.VaultRoleAdmin,
	})

	err := uc.Delete(context.Background(), adminID, created.ID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestVaultDelete_NotMember(t *testing.T) {
	uc, _, _, _, _ := newTestVaultUsecase()
	ownerID := uuid.New()
	created := createTestVault(t, uc, ownerID)

	err := uc.Delete(context.Background(), uuid.New(), created.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}
