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

type inviteTestSetup struct {
	uc         *usecase.VaultInviteUsecase
	vaultUC    *usecase.VaultUsecase
	memberRepo *fake.VaultMemberRepository
	keyRepo    *fake.VaultKeyRepository
	inviteRepo *fake.VaultInviteRepository
	userRepo   *fake.UserRepository
	vaultRepo  *fake.VaultRepository
}

func newInviteTestSetup() *inviteTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	userRepo := fake.NewUserRepository()

	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	inviteUC := usecase.NewVaultInviteUsecase(inviteRepo, memberRepo, keyRepo, userRepo, vaultRepo, fake.NewTransactor())

	return &inviteTestSetup{
		uc:         inviteUC,
		vaultUC:    vaultUC,
		memberRepo: memberRepo,
		keyRepo:    keyRepo,
		inviteRepo: inviteRepo,
		userRepo:   userRepo,
		vaultRepo:  vaultRepo,
	}
}

func TestInviteCreate_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invite, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           vault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("encrypted-key"),
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, invite.ID)
	assert.Equal(t, vault.ID, invite.VaultID)
	assert.Equal(t, "invitee@test.com", invite.InviteeEmail)
	assert.Equal(t, domain.VaultRoleEditor, invite.Role)
	assert.Equal(t, "Test Vault", invite.VaultName)
	assert.False(t, invite.ExpiresAt.IsZero())
}

func TestInviteCreate_EmptyEmail(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           vault.ID,
		InviteeEmail:      "",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestInviteCreate_OwnerRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           vault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleOwner,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestInviteCreate_EmptyKey(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:      owner.ID,
		VaultID:      vault.ID,
		InviteeEmail: "invitee@test.com",
		Role:         domain.VaultRoleEditor,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestInviteCreate_NotMember(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:           uuid.New(),
		VaultID:           vault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestInviteCreate_InsufficientRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	_, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:           editor.ID,
		VaultID:           vault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleViewer,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestInviteCreate_AlreadyMember(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	existing := seedUser(t, s.userRepo, "existing@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vault.ID, UserID: existing.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	_, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           vault.ID,
		InviteeEmail:      "existing@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

func TestInviteListByVault_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "a@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "b@test.com",
		Role: domain.VaultRoleViewer, EncryptedVaultKey: []byte("key"),
	})

	invites, err := s.uc.ListByVault(context.Background(), owner.ID, vault.ID)
	require.NoError(t, err)
	assert.Len(t, invites, 2)
}

func TestInviteListByVault_InsufficientRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	viewer := seedUser(t, s.userRepo, "viewer@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vault.ID, UserID: viewer.ID, Role: domain.VaultRoleViewer, JoinedAt: time.Now(),
	})

	_, err := s.uc.ListByVault(context.Background(), viewer.ID, vault.ID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestInviteListMyInvites_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	invites, err := s.uc.ListMyInvites(context.Background(), "invitee@test.com")
	require.NoError(t, err)
	assert.Len(t, invites, 1)
	assert.Equal(t, "Test Vault", invites[0].VaultName)
}

func TestInviteListMyInvites_FiltersAccepted(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	s.inviteRepo.Accept(context.Background(), invite.ID)

	invites, err := s.uc.ListMyInvites(context.Background(), "invitee@test.com")
	require.NoError(t, err)
	assert.Empty(t, invites)
}

func TestInviteAccept_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	invite, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("encrypted-vault-key"),
	})
	require.NoError(t, err)

	vaultInfo, err := s.uc.Accept(context.Background(), usecase.AcceptInviteInput{
		UserID:   invitee.ID,
		InviteID: invite.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, vault.ID, vaultInfo.ID)
	assert.Equal(t, domain.VaultRoleEditor, vaultInfo.Role)

	member, err := s.memberRepo.GetByVaultAndUser(context.Background(), vault.ID, invitee.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VaultRoleEditor, member.Role)

	key, err := s.keyRepo.GetByVaultAndUser(context.Background(), vault.ID, invitee.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("encrypted-vault-key"), key.EncryptedKey)
}

func TestInviteAccept_WrongUser(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	wrongUser := seedUser(t, s.userRepo, "wrong@test.com")

	invite, err := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	require.NoError(t, err)

	_, err = s.uc.Accept(context.Background(), usecase.AcceptInviteInput{
		UserID:   wrongUser.ID,
		InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestInviteAccept_AlreadyAccepted(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	invite, _ := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	_, err := s.uc.Accept(context.Background(), usecase.AcceptInviteInput{
		UserID: invitee.ID, InviteID: invite.ID,
	})
	require.NoError(t, err)

	_, err = s.uc.Accept(context.Background(), usecase.AcceptInviteInput{
		UserID: invitee.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

func TestInviteAccept_Expired(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	expiredInvite := &domain.VaultInvite{
		ID:                uuid.New(),
		VaultID:           vault.ID,
		InviterID:         owner.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
		ExpiresAt:         time.Now().Add(-1 * time.Hour),
		CreatedAt:         time.Now().Add(-8 * 24 * time.Hour),
	}
	s.inviteRepo.Create(context.Background(), expiredInvite)

	_, err := s.uc.Accept(context.Background(), usecase.AcceptInviteInput{
		UserID: invitee.ID, InviteID: expiredInvite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInviteExpired)
}

func TestInviteAccept_NotFound(t *testing.T) {
	s := newInviteTestSetup()
	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	_, err := s.uc.Accept(context.Background(), usecase.AcceptInviteInput{
		UserID: invitee.ID, InviteID: uuid.New(),
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInviteDelete_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), usecase.DeleteInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteID: invite.ID,
	})
	require.NoError(t, err)

	_, err = s.inviteRepo.GetByID(context.Background(), invite.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInviteDelete_NotMember(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), usecase.DeleteInviteInput{
		ActorID: uuid.New(), VaultID: vault.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestInviteDelete_InsufficientRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	invite, _ := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), usecase.DeleteInviteInput{
		ActorID: editor.ID, VaultID: vault.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestInviteDelete_WrongVault(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault1 := createTestVault(t, s.vaultUC, owner.ID)
	vault2 := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), usecase.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault1.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), usecase.DeleteInviteInput{
		ActorID: owner.ID, VaultID: vault2.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
