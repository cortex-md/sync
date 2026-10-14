package vault_test

import (
	"context"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type accessSetup struct {
	ctx     context.Context
	access  *vault.AccessService
	members *vault.MemberService
	repo    *fake.VaultMemberRepository
	files   *fake.FileLatestRepository
	authz   *authz.Authorizer
	signals *[]bus.Signal
	vaultID uuid.UUID
	users   map[domain.VaultRole]uuid.UUID
}

func newAccessSetup(t *testing.T) *accessSetup {
	t.Helper()
	repo := fake.NewVaultMemberRepository()
	files := fake.NewFileLatestRepository()
	repo.ResolveFilesWith(files)
	signals := &[]bus.Signal{}
	memory := bus.NewMemory()
	memory.Subscribe(bus.KindVaultAccessChanged, func(s bus.Signal) { *signals = append(*signals, s) })
	authorizer := authz.NewCached(repo)
	s := &accessSetup{
		ctx:     context.Background(),
		repo:    repo,
		files:   files,
		authz:   authorizer,
		signals: signals,
		vaultID: uuid.New(),
		users:   map[domain.VaultRole]uuid.UUID{},
	}
	s.access = vault.NewAccessService(vault.AccessDeps{Access: repo, Members: repo, Tx: fake.NewTransactor(), Authz: authorizer, Bus: memory})
	s.members = vault.NewMemberService(vault.MemberDeps{
		Members: repo, Keys: fake.NewVaultKeyRepository(), Users: fake.NewUserRepository(), Access: repo,
		Tx: fake.NewTransactor(), Authz: authorizer, Bus: memory,
	})
	for _, role := range []domain.VaultRole{domain.VaultRoleOwner, domain.VaultRoleEditor, domain.VaultRoleViewer, domain.VaultRoleGuest} {
		id := uuid.New()
		s.users[role] = id
		require.NoError(t, repo.Add(s.ctx, &domain.VaultMember{VaultID: s.vaultID, UserID: id, Role: role, JoinedAt: time.Now()}))
	}
	return s
}

func (s *accessSetup) owner() uuid.UUID { return s.users[domain.VaultRoleOwner] }

func (s *accessSetup) scope(t *testing.T, role domain.VaultRole) domain.Scope {
	t.Helper()
	access, err := s.authz.Access(s.ctx, s.vaultID, s.users[role], domain.PermFileRead)
	require.NoError(t, err)
	return access.Scope
}

func (s *accessSetup) liveFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, s.files.Upsert(s.ctx, &domain.FileLatest{VaultID: s.vaultID, FilePath: path, CurrentVersion: 1}))
}

func TestAccessGroupsGrantRestrictedFolder(t *testing.T) {
	s := newAccessSetup(t)
	editor, viewer := s.users[domain.VaultRoleEditor], s.users[domain.VaultRoleViewer]

	_, err := s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "finance/", Restricted: true})
	require.NoError(t, err)
	assert.False(t, s.scope(t, domain.VaultRoleEditor).CanRead("finance/q1.md"))
	assert.True(t, s.scope(t, domain.VaultRoleOwner).CanWrite("finance/q1.md"))

	group, err := s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, Name: " Finance ", MemberIDs: []uuid.UUID{editor, viewer, editor}})
	require.NoError(t, err)
	assert.Equal(t, "Finance", group.Name)
	assert.Len(t, group.MemberIDs, 2)

	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "finance/", Restricted: true, Entries: []vault.AccessEntryInput{
		{PrincipalType: "group", PrincipalID: group.ID, Level: "view"},
		{PrincipalType: "user", PrincipalID: viewer, Level: "edit"},
	}})
	require.NoError(t, err)

	assert.True(t, s.scope(t, domain.VaultRoleEditor).CanRead("finance/q1.md"))
	assert.False(t, s.scope(t, domain.VaultRoleEditor).CanWrite("finance/q1.md"))
	assert.True(t, s.scope(t, domain.VaultRoleViewer).CanWrite("finance/q1.md"))
	assert.False(t, s.scope(t, domain.VaultRoleGuest).CanRead("finance/q1.md"))

	members := []uuid.UUID{viewer}
	_, err = s.access.UpdateGroup(s.ctx, vault.UpdateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, GroupID: group.ID, MemberIDs: &members})
	require.NoError(t, err)
	assert.False(t, s.scope(t, domain.VaultRoleEditor).CanRead("finance/q1.md"))

	require.NoError(t, s.access.DeleteGroup(s.ctx, s.owner(), s.vaultID, group.ID))
	rules, err := s.access.ListRules(s.ctx, s.owner(), s.vaultID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Len(t, rules[0].Entries, 1)
	assert.Equal(t, 5, len(*s.signals))
}

func TestAccessMutationsRequireMemberManage(t *testing.T) {
	s := newAccessSetup(t)
	editor := s.users[domain.VaultRoleEditor]
	_, err := s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: editor, VaultID: s.vaultID, Name: "x"})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: editor, VaultID: s.vaultID, Path: "a/", Restricted: true})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
	_, err = s.access.ListRules(s.ctx, editor, s.vaultID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)
	_, err = s.access.ListGroups(s.ctx, editor, s.vaultID)
	assert.NoError(t, err)
}

func TestAccessValidation(t *testing.T) {
	s := newAccessSetup(t)
	_, err := s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, Name: "Team"})
	require.NoError(t, err)
	_, err = s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, Name: "team"})
	assert.ErrorIs(t, err, domain.ErrGroupNameTaken)
	_, err = s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, Name: "   "})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
	_, err = s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, Name: "Other", MemberIDs: []uuid.UUID{uuid.New()}})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "a/", Entries: []vault.AccessEntryInput{
		{PrincipalType: "user", PrincipalID: uuid.New(), Level: "view"},
	}})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "a/", Entries: []vault.AccessEntryInput{
		{PrincipalType: "group", PrincipalID: uuid.New(), Level: "view"},
	}})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "../x", Restricted: true})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	rule, err := s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "/a/", Restricted: false})
	require.NoError(t, err)
	assert.Nil(t, rule)
}

func TestRemovingMemberDropsEntriesAndGroupMembership(t *testing.T) {
	s := newAccessSetup(t)
	viewer := s.users[domain.VaultRoleViewer]
	group, err := s.access.CreateGroup(s.ctx, vault.CreateGroupInput{ActorID: s.owner(), VaultID: s.vaultID, Name: "G", MemberIDs: []uuid.UUID{viewer}})
	require.NoError(t, err)
	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "x.md", Entries: []vault.AccessEntryInput{
		{PrincipalType: "user", PrincipalID: viewer, Level: "edit"},
	}})
	require.NoError(t, err)

	require.NoError(t, s.members.Remove(s.ctx, vault.RemoveMemberInput{ActorID: s.owner(), VaultID: s.vaultID, UserID: viewer}))

	groups, err := s.access.ListGroups(s.ctx, s.owner(), s.vaultID)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, group.ID, groups[0].ID)
	assert.Empty(t, groups[0].MemberIDs)
	rules, err := s.access.ListRules(s.ctx, s.owner(), s.vaultID)
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestMovePathCarriesFolderRulesAndExactFileRules(t *testing.T) {
	s := newAccessSetup(t)
	_, err := s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "secret/", Restricted: true})
	require.NoError(t, err)
	_, err = s.access.PutRule(s.ctx, vault.PutRuleInput{ActorID: s.owner(), VaultID: s.vaultID, Path: "secret/plan.md", Restricted: true, Entries: []vault.AccessEntryInput{
		{PrincipalType: "user", PrincipalID: s.users[domain.VaultRoleViewer], Level: "edit"},
	}})
	require.NoError(t, err)
	s.liveFile(t, "secret/plan.md")
	s.liveFile(t, "secret/notes.md")
	s.liveFile(t, "open/readme.md")

	changed, err := s.access.MovePath(s.ctx, s.vaultID, "secret/plan.md", "renamed/plan.md")
	require.NoError(t, err)
	assert.True(t, changed)
	paths := rulePaths(t, s)
	assert.ElementsMatch(t, []string{"secret/", "renamed/", "renamed/plan.md"}, paths)
	assert.True(t, s.scope(t, domain.VaultRoleViewer).CanWrite("renamed/plan.md"))
	assert.False(t, s.scope(t, domain.VaultRoleEditor).CanRead("renamed/other.md"))

	changed, err = s.access.MovePath(s.ctx, s.vaultID, "secret/notes.md", "open/notes.md")
	require.NoError(t, err)
	assert.False(t, changed, "moving into a folder that already has files must not restrict it")
	assert.NotContains(t, rulePaths(t, s), "open/")

	removed, err := s.access.RemoveForPath(s.ctx, s.vaultID, "renamed/plan.md")
	require.NoError(t, err)
	assert.True(t, removed)
	assert.ElementsMatch(t, []string{"secret/", "renamed/"}, rulePaths(t, s))
}

func rulePaths(t *testing.T, s *accessSetup) []string {
	t.Helper()
	rules, err := s.access.ListRules(s.ctx, s.owner(), s.vaultID)
	require.NoError(t, err)
	var paths []string
	for _, r := range rules {
		paths = append(paths, r.Path)
	}
	return paths
}
