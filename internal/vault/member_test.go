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

type memberTestSetup struct {
	uc         *vault.MemberService
	vaultUC    *vault.Service
	memberRepo *fake.VaultMemberRepository
	keyRepo    *fake.VaultKeyRepository
	userRepo   *fake.UserRepository
}

func newMemberTestSetup() *memberTestSetup {
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	keyRepo := fake.NewVaultKeyRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	userRepo := fake.NewUserRepository()
	vaultRepo.ResolveSummariesWith(memberRepo, keyRepo)
	inviteRepo.ResolveDetailsWith(vaultRepo, userRepo)
	vaultUC := vault.NewService(vault.Deps{Vaults: vaultRepo, Members: memberRepo, Keys: keyRepo, Invites: inviteRepo, Tx: fake.NewTransactor()})
	memberUC := vault.NewMemberService(vault.MemberDeps{Members: memberRepo, Keys: keyRepo, Users: userRepo})

	return &memberTestSetup{
		uc:         memberUC,
		vaultUC:    vaultUC,
		memberRepo: memberRepo,
		keyRepo:    keyRepo,
		userRepo:   userRepo,
	}
}

func seedUser(t *testing.T, userRepo *fake.UserRepository, email string) *domain.User {
	t.Helper()
	user := &domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: "hash",
		DisplayName:  "User " + email,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	err := userRepo.Create(context.Background(), user)
	require.NoError(t, err)
	return user
}

func TestMemberList_Success(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID:  testVault.ID,
		UserID:   editor.ID,
		Role:     domain.VaultRoleEditor,
		JoinedAt: time.Now(),
	})

	members, err := s.uc.List(context.Background(), owner.ID, testVault.ID)
	require.NoError(t, err)
	assert.Len(t, members, 2)
}

func TestMemberList_NotMember(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	_, err := s.uc.List(context.Background(), uuid.New(), testVault.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestMemberList_IncludesUserInfo(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	members, err := s.uc.List(context.Background(), owner.ID, testVault.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "owner@test.com", members[0].Email)
	assert.Equal(t, "User owner@test.com", members[0].DisplayName)
}

func TestMemberUpdateRole_OwnerCanPromote(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	err := s.uc.UpdateRole(context.Background(), vault.UpdateMemberRoleInput{
		ActorID: owner.ID,
		VaultID: testVault.ID,
		UserID:  editor.ID,
		Role:    domain.VaultRoleAdmin,
	})
	require.NoError(t, err)

	member, err := s.memberRepo.GetByVaultAndUser(context.Background(), testVault.ID, editor.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VaultRoleAdmin, member.Role)
}

func TestMemberUpdateRole_CannotChangeOwner(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	admin := seedUser(t, s.userRepo, "admin@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: admin.ID, Role: domain.VaultRoleAdmin, JoinedAt: time.Now(),
	})

	err := s.uc.UpdateRole(context.Background(), vault.UpdateMemberRoleInput{
		ActorID: admin.ID,
		VaultID: testVault.ID,
		UserID:  owner.ID,
		Role:    domain.VaultRoleEditor,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestMemberUpdateRole_CannotAssignOwnerRole(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	err := s.uc.UpdateRole(context.Background(), vault.UpdateMemberRoleInput{
		ActorID: owner.ID,
		VaultID: testVault.ID,
		UserID:  editor.ID,
		Role:    domain.VaultRoleOwner,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestMemberUpdateRole_EditorCannotUpdateRoles(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	viewer := seedUser(t, s.userRepo, "viewer@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: viewer.ID, Role: domain.VaultRoleViewer, JoinedAt: time.Now(),
	})

	err := s.uc.UpdateRole(context.Background(), vault.UpdateMemberRoleInput{
		ActorID: editor.ID,
		VaultID: testVault.ID,
		UserID:  viewer.ID,
		Role:    domain.VaultRoleEditor,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestMemberUpdateRole_NotMember(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	err := s.uc.UpdateRole(context.Background(), vault.UpdateMemberRoleInput{
		ActorID: uuid.New(),
		VaultID: testVault.ID,
		UserID:  owner.ID,
		Role:    domain.VaultRoleViewer,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestMemberRemove_OwnerRemovesEditor(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})
	s.keyRepo.Upsert(context.Background(), &domain.VaultKey{
		VaultID: testVault.ID, UserID: editor.ID, EncryptedKey: []byte("key"),
	})

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: owner.ID,
		VaultID: testVault.ID,
		UserID:  editor.ID,
	})
	require.NoError(t, err)

	_, err = s.memberRepo.GetByVaultAndUser(context.Background(), testVault.ID, editor.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = s.keyRepo.GetByVaultAndUser(context.Background(), testVault.ID, editor.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestMemberRemove_OwnerCannotRemoveSelf(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: owner.ID,
		VaultID: testVault.ID,
		UserID:  owner.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestMemberRemove_EditorLeavesVoluntarily(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: editor.ID,
		VaultID: testVault.ID,
		UserID:  editor.ID,
	})
	require.NoError(t, err)

	_, err = s.memberRepo.GetByVaultAndUser(context.Background(), testVault.ID, editor.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestMemberRemove_CannotRemoveOwner(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	admin := seedUser(t, s.userRepo, "admin@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: admin.ID, Role: domain.VaultRoleAdmin, JoinedAt: time.Now(),
	})

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: admin.ID,
		VaultID: testVault.ID,
		UserID:  owner.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestMemberRemove_AdminCannotRemoveAdmin(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	admin1 := seedUser(t, s.userRepo, "admin1@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: admin1.ID, Role: domain.VaultRoleAdmin, JoinedAt: time.Now(),
	})

	admin2 := seedUser(t, s.userRepo, "admin2@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: admin2.ID, Role: domain.VaultRoleAdmin, JoinedAt: time.Now(),
	})

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: admin1.ID,
		VaultID: testVault.ID,
		UserID:  admin2.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestMemberRemove_EditorCannotRemoveOthers(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	editor := seedUser(t, s.userRepo, "editor@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: editor.ID, Role: domain.VaultRoleEditor, JoinedAt: time.Now(),
	})

	viewer := seedUser(t, s.userRepo, "viewer@test.com")
	s.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: testVault.ID, UserID: viewer.ID, Role: domain.VaultRoleViewer, JoinedAt: time.Now(),
	})

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: editor.ID,
		VaultID: testVault.ID,
		UserID:  viewer.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
}

func TestMemberRemove_NotMember(t *testing.T) {
	s := newMemberTestSetup()
	owner := seedUser(t, s.userRepo, "owner@test.com")
	testVault := createTestVault(t, s.vaultUC, owner.ID)

	err := s.uc.Remove(context.Background(), vault.RemoveMemberInput{
		ActorID: uuid.New(),
		VaultID: testVault.ID,
		UserID:  owner.ID,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}
