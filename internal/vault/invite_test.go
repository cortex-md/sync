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

type inviteTestSetup struct {
	uc         *vault.InviteService
	vaultUC    *vault.Service
	memberRepo *fake.VaultMemberRepository
	keyRepo    *fake.VaultKeyRepository
	inviteRepo *fake.VaultInviteRepository
	userRepo   *fake.UserRepository
	vaultRepo  *fake.VaultRepository
}

func newInviteTestSetup(opts ...func(*vault.InviteDeps)) *inviteTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	userRepo := fake.NewUserRepository()
	vaultRepo.ResolveSummariesWith(memberRepo, keyRepo)
	inviteRepo.ResolveDetailsWith(vaultRepo, userRepo)
	vaultUC := vault.NewService(vault.Deps{Vaults: vaultRepo, Members: memberRepo, Keys: keyRepo, Invites: inviteRepo, Tx: fake.NewTransactor()})
	deps := vault.InviteDeps{Invites: inviteRepo, Members: memberRepo, Keys: keyRepo, Users: userRepo, Vaults: vaultRepo, Tx: fake.NewTransactor()}
	for _, opt := range opts {
		opt(&deps)
	}
	inviteUC := vault.NewInviteService(deps)

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
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invite, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           testVault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("encrypted-key"),
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, invite.ID)
	assert.Equal(t, testVault.ID, invite.VaultID)
	assert.Equal(t, "invitee@test.com", invite.InviteeEmail)
	assert.Equal(t, domain.VaultRoleEditor, invite.Role)
	assert.Equal(t, "Test Vault", invite.VaultName)
	assert.False(t, invite.ExpiresAt.IsZero())
}

func TestInviteCreate_EmptyEmail(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           testVault.ID,
		InviteeEmail:      "",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestInviteCreate_OwnerRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           testVault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleOwner,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestInviteWithoutKey_AcceptedByTokenLinkIsSingleUse(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	invitee := seedUser(t, s.userRepo, "other-address@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invite, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:      owner.ID,
		VaultID:      testVault.ID,
		InviteeEmail: "invitee@test.com",
		Role:         domain.VaultRoleEditor,
	})
	require.NoError(t, err)
	require.NotEmpty(t, invite.Token)

	preview, err := s.uc.Preview(context.Background(), invite.Token)
	require.NoError(t, err)
	assert.Equal(t, testVault.Name, preview.VaultName)

	accepted, err := s.uc.Accept(context.Background(), vault.AcceptInviteInput{UserID: invitee.ID, Token: invite.Token})
	require.NoError(t, err)
	assert.Equal(t, testVault.ID, accepted.Vault.ID)
	assert.Empty(t, accepted.EncryptedVaultKey)

	member, err := s.memberRepo.GetByVaultAndUser(context.Background(), testVault.ID, invitee.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VaultRoleEditor, member.Role)

	_, err = s.uc.Accept(context.Background(), vault.AcceptInviteInput{UserID: invitee.ID, Token: invite.Token})
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

func TestInviteCreate_NotMember(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:           uuid.New(),
		VaultID:           testVault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestInviteCreate_InsufficientRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	_, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:           editor.ID,
		VaultID:           testVault.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleViewer,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestInviteCreate_AlreadyMember(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	existing := seedUser(t, s.userRepo, "existing@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: existing.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	_, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID:           owner.ID,
		VaultID:           testVault.ID,
		InviteeEmail:      "existing@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
	})
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

func TestInviteListByVault_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "a@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "b@test.com",
		Role: domain.VaultRoleViewer, EncryptedVaultKey: []byte("key"),
	})

	invites, err := s.uc.ListByVault(context.Background(), owner.ID, testVault.ID)
	require.NoError(t, err)
	assert.Len(t, invites, 2)
}

func TestInviteListByVault_InsufficientRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	viewer := seedUser(t, s.userRepo, "viewer@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: viewer.ID, Role: domain.VaultRoleViewer, JoinedAt: time.Now(),
	})

	_, err := s.uc.ListByVault(context.Background(), viewer.ID, testVault.ID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestInviteListMyInvites_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	invites, err := s.uc.ListMyInvites(context.Background(), invitee.ID)
	require.NoError(t, err)
	assert.Len(t, invites, 1)
	assert.Equal(t, "Test Vault", invites[0].VaultName)
}

func TestInviteListMyInvites_FiltersAccepted(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	s.inviteRepo.Accept(context.Background(), invite.ID)
	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	invites, err := s.uc.ListMyInvites(context.Background(), invitee.ID)
	require.NoError(t, err)
	assert.Empty(t, invites)
}

func TestInviteAccept_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	invite, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("encrypted-vault-key"),
	})
	require.NoError(t, err)

	vaultInfo, err := s.uc.Accept(context.Background(), vault.AcceptInviteInput{
		UserID:   invitee.ID,
		InviteID: invite.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, testVault.ID, vaultInfo.Vault.ID)
	assert.Equal(t, domain.VaultRoleEditor, vaultInfo.Vault.Role)

	member, err := s.memberRepo.GetByVaultAndUser(context.Background(), testVault.ID, invitee.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VaultRoleEditor, member.Role)

	assert.Equal(t, []byte("encrypted-vault-key"), vaultInfo.EncryptedVaultKey)
	_, err = s.keyRepo.GetByVaultAndUser(context.Background(), testVault.ID, invitee.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInviteAccept_WrongUser(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	wrongUser := seedUser(t, s.userRepo, "wrong@test.com")

	invite, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})
	require.NoError(t, err)

	_, err = s.uc.Accept(context.Background(), vault.AcceptInviteInput{
		UserID:   wrongUser.ID,
		InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestInviteAccept_AlreadyAccepted(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	invite, _ := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	_, err := s.uc.Accept(context.Background(), vault.AcceptInviteInput{
		UserID: invitee.ID, InviteID: invite.ID,
	})
	require.NoError(t, err)

	_, err = s.uc.Accept(context.Background(), vault.AcceptInviteInput{
		UserID: invitee.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

func TestInviteAccept_Expired(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	expiredInvite := &domain.VaultInvite{
		ID:                uuid.New(),
		VaultID:           testVault.ID,
		InviterID:         owner.ID,
		InviteeEmail:      "invitee@test.com",
		Role:              domain.VaultRoleEditor,
		EncryptedVaultKey: []byte("key"),
		ExpiresAt:         time.Now().Add(-1 * time.Hour),
		CreatedAt:         time.Now().Add(-8 * 24 * time.Hour),
	}
	s.inviteRepo.Create(context.Background(), expiredInvite)

	_, err := s.uc.Accept(context.Background(), vault.AcceptInviteInput{
		UserID: invitee.ID, InviteID: expiredInvite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInviteExpired)
}

func TestInviteAccept_NotFound(t *testing.T) {
	s := newInviteTestSetup()
	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	_, err := s.uc.Accept(context.Background(), vault.AcceptInviteInput{
		UserID: invitee.ID, InviteID: uuid.New(),
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInviteDelete_Success(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), vault.DeleteInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteID: invite.ID,
	})
	require.NoError(t, err)

	_, err = s.inviteRepo.GetByID(context.Background(), invite.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInviteDelete_NotMember(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), vault.DeleteInviteInput{
		ActorID: uuid.New(), VaultID: testVault.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestInviteDelete_InsufficientRole(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	invite, _ := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), vault.DeleteInviteInput{
		ActorID: editor.ID, VaultID: testVault.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestInviteDelete_WrongVault(t *testing.T) {
	s := newInviteTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	vault1 := createTestVault(t, s.vaultUC, owner.ID)
	vault2 := createTestVault(t, s.vaultUC, owner.ID)

	invite, _ := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: vault1.ID, InviteeEmail: "invitee@test.com",
		Role: domain.VaultRoleEditor, EncryptedVaultKey: []byte("key"),
	})

	err := s.uc.Delete(context.Background(), vault.DeleteInviteInput{
		ActorID: owner.ID, VaultID: vault2.ID, InviteID: invite.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInviteEmailTrustRequiresVerifiedEmail(t *testing.T) {
	s := newInviteTestSetup(func(d *vault.InviteDeps) { d.EmailTrust = vault.EmailTrustVerified })
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)
	invite, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "squatter@test.com", Role: domain.VaultRoleAdmin,
	})
	require.NoError(t, err)
	squatter := seedUser(t, s.userRepo, "squatter@test.com")

	listed, err := s.uc.ListMyInvites(context.Background(), squatter.ID)
	require.NoError(t, err)
	assert.Empty(t, listed)
	_, err = s.uc.Accept(context.Background(), vault.AcceptInviteInput{UserID: squatter.ID, InviteID: invite.ID})
	assert.ErrorIs(t, err, domain.ErrEmailNotVerified)
}

func TestInviteEmailTrustNoneOnlyAcceptsTokens(t *testing.T) {
	s := newInviteTestSetup(func(d *vault.InviteDeps) { d.EmailTrust = vault.EmailTrustNone })
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)
	invite, err := s.uc.Create(context.Background(), vault.CreateInviteInput{
		ActorID: owner.ID, VaultID: testVault.ID, InviteeEmail: "invitee@test.com", Role: domain.VaultRoleEditor,
	})
	require.NoError(t, err)
	invitee := seedUser(t, s.userRepo, "invitee@test.com")

	_, err = s.uc.Accept(context.Background(), vault.AcceptInviteInput{UserID: invitee.ID, InviteID: invite.ID})
	assert.ErrorIs(t, err, domain.ErrFeatureDisabled)
}
