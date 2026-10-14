package template_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/template"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type templateSetup struct {
	ctx       context.Context
	service   *template.Service
	templates *fake.TemplateRepository
	members   *fake.VaultMemberRepository
	vaults    *fake.VaultRepository
	vaultID   uuid.UUID
	users     map[domain.VaultRole]uuid.UUID
	editor2   uuid.UUID
}

func newTemplateSetup(t *testing.T) *templateSetup {
	t.Helper()
	members := fake.NewVaultMemberRepository()
	vaults := fake.NewVaultRepository()
	users := fake.NewUserRepository()
	templates := fake.NewTemplateRepository()
	s := &templateSetup{
		ctx:       context.Background(),
		templates: templates,
		members:   members,
		vaults:    vaults,
		vaultID:   uuid.New(),
		users:     map[domain.VaultRole]uuid.UUID{},
		editor2:   uuid.New(),
	}
	require.NoError(t, vaults.Create(s.ctx, &domain.Vault{ID: s.vaultID, Name: "Team", AllowPublicLinks: true}))
	add := func(id uuid.UUID, role domain.VaultRole, name string) {
		require.NoError(t, members.Add(s.ctx, &domain.VaultMember{VaultID: s.vaultID, UserID: id, Role: role, JoinedAt: time.Now()}))
		require.NoError(t, users.Create(s.ctx, &domain.User{ID: id, Email: name + "@example.com", DisplayName: name}))
	}
	for _, role := range []domain.VaultRole{domain.VaultRoleOwner, domain.VaultRoleAdmin, domain.VaultRoleEditor, domain.VaultRoleViewer, domain.VaultRoleGuest} {
		id := uuid.New()
		s.users[role] = id
		add(id, role, string(role))
	}
	add(s.editor2, domain.VaultRoleEditor, "editor2")
	s.service = template.NewService(template.Deps{
		Templates: templates,
		Groups:    members,
		Users:     users,
		Vaults:    vaults,
		Tx:        fake.NewTransactor(),
		Authz:     authz.New(members),
	})
	return s
}

func (s *templateSetup) group(t *testing.T, name string, memberIDs ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, s.members.CreateGroup(s.ctx, &domain.VaultGroup{ID: id, VaultID: s.vaultID, Name: name, MemberIDs: memberIDs}))
	return id
}

func (s *templateSetup) editor() uuid.UUID { return s.users[domain.VaultRoleEditor] }

func templatePayload(t *testing.T, kind string, paths ...string) []byte {
	t.Helper()
	files := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		files = append(files, map[string]string{"path": path, "content": "# " + path})
	}
	data, err := json.Marshal(map[string]any{"format": 1, "kind": kind, "files": files})
	require.NoError(t, err)
	return data
}

func (s *templateSetup) create(t *testing.T, input template.CreateInput) *template.View {
	t.Helper()
	if input.UserID == uuid.Nil {
		input.UserID = s.editor()
	}
	input.VaultID = s.vaultID
	if input.Kind == "" {
		input.Kind = domain.TemplateKindNote
	}
	if input.Name == "" {
		input.Name = "Meeting notes"
	}
	if input.Payload == nil {
		if input.Audience == domain.TemplateAudienceTeam {
			input.Payload = []byte("ciphertext")
			input.Encrypted = true
		} else {
			input.Payload = templatePayload(t, string(input.Kind), "Meeting.md")
		}
	}
	view, err := s.service.Create(s.ctx, input)
	require.NoError(t, err)
	return view
}

func visibleIDs(t *testing.T, s *templateSetup, userID uuid.UUID) []uuid.UUID {
	t.Helper()
	views, err := s.service.List(s.ctx, userID, s.vaultID)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	return ids
}

func TestCreateValidatesEncryptionPerAudience(t *testing.T) {
	s := newTemplateSetup(t)
	cases := []struct {
		audience  domain.TemplateAudience
		encrypted bool
	}{
		{domain.TemplateAudienceTeam, false},
		{domain.TemplateAudienceLink, true},
		{domain.TemplateAudienceMarket, true},
	}
	for _, tc := range cases {
		_, err := s.service.Create(s.ctx, template.CreateInput{
			UserID: s.editor(), VaultID: s.vaultID, Kind: domain.TemplateKindNote, Audience: tc.audience,
			Name: "x", Payload: templatePayload(t, "note", "a.md"), Encrypted: tc.encrypted,
		})
		assert.ErrorIs(t, err, domain.ErrInvalidInput, tc.audience)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	s := newTemplateSetup(t)
	base := template.CreateInput{
		UserID: s.editor(), VaultID: s.vaultID, Kind: domain.TemplateKindNote, Audience: domain.TemplateAudienceLink,
		Name: "Template", Payload: templatePayload(t, "note", "a.md"),
	}
	mutations := map[string]func(*template.CreateInput){
		"kind":        func(in *template.CreateInput) { in.Kind = "page" },
		"audience":    func(in *template.CreateInput) { in.Audience = "world" },
		"name":        func(in *template.CreateInput) { in.Name = "   " },
		"payload":     func(in *template.CreateInput) { in.Payload = nil },
		"group link":  func(in *template.CreateInput) { id := uuid.New(); in.GroupID = &id },
		"description": func(in *template.CreateInput) { in.Description = strings.Repeat("d", 2001) },
	}
	for name, mutate := range mutations {
		input := base
		mutate(&input)
		_, err := s.service.Create(s.ctx, input)
		assert.ErrorIs(t, err, domain.ErrInvalidInput, name)
	}

	unknownGroup := uuid.New()
	_, err := s.service.Create(s.ctx, template.CreateInput{
		UserID: s.editor(), VaultID: s.vaultID, Kind: domain.TemplateKindNote, Audience: domain.TemplateAudienceTeam,
		GroupID: &unknownGroup, Name: "x", Payload: []byte("c"), Encrypted: true,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	big := base
	big.Payload = make([]byte, template.MaxPayloadBytes+1)
	_, err = s.service.Create(s.ctx, big)
	assert.ErrorIs(t, err, domain.ErrFileTooLarge)
}

func TestCreateRequiresEditorRole(t *testing.T) {
	s := newTemplateSetup(t)
	for _, role := range []domain.VaultRole{domain.VaultRoleViewer, domain.VaultRoleGuest} {
		_, err := s.service.Create(s.ctx, template.CreateInput{
			UserID: s.users[role], VaultID: s.vaultID, Kind: domain.TemplateKindNote, Audience: domain.TemplateAudienceTeam,
			Name: "x", Payload: []byte("c"), Encrypted: true,
		})
		assert.ErrorIs(t, err, domain.ErrInsufficientRole, role)
	}
	_, err := s.service.Create(s.ctx, template.CreateInput{
		UserID: uuid.New(), VaultID: s.vaultID, Kind: domain.TemplateKindNote, Audience: domain.TemplateAudienceTeam,
		Name: "x", Payload: []byte("c"), Encrypted: true,
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)

	view := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})
	assert.Equal(t, domain.TemplateStatusPublished, view.Status)
	assert.Equal(t, "editor", view.OwnerName)
	assert.Equal(t, 1, view.Version)
	assert.Len(t, view.Slug, 12)
}

func TestListVisibility(t *testing.T) {
	s := newTemplateSetup(t)
	viewer := s.users[domain.VaultRoleViewer]
	design := s.group(t, "Design", viewer)
	everyone := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})
	grouped := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam, GroupID: &design})
	link := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceLink})

	require.NotNil(t, grouped.GroupName)
	assert.Equal(t, "Design", *grouped.GroupName)

	assert.ElementsMatch(t, []uuid.UUID{everyone.ID, grouped.ID, link.ID}, visibleIDs(t, s, s.editor()))
	assert.ElementsMatch(t, []uuid.UUID{everyone.ID, grouped.ID}, visibleIDs(t, s, viewer))
	assert.ElementsMatch(t, []uuid.UUID{everyone.ID}, visibleIDs(t, s, s.editor2))
	assert.ElementsMatch(t, []uuid.UUID{everyone.ID, grouped.ID, link.ID}, visibleIDs(t, s, s.users[domain.VaultRoleAdmin]))

	_, err := s.service.List(s.ctx, s.users[domain.VaultRoleGuest], s.vaultID)
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)

	_, err = s.service.GetPayload(s.ctx, s.editor2, s.vaultID, grouped.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	payload, err := s.service.GetPayload(s.ctx, viewer, s.vaultID, grouped.ID)
	require.NoError(t, err)
	assert.True(t, payload.Encrypted)
	assert.Equal(t, []byte("ciphertext"), payload.Data)

	_, err = s.service.GetPayload(s.ctx, s.editor(), uuid.New(), grouped.ID)
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}

func TestMarketReviewPublishesValidPayload(t *testing.T) {
	s := newTemplateSetup(t)
	view := s.create(t, template.CreateInput{
		Audience: domain.TemplateAudienceMarket, Kind: domain.TemplateKindDatabase, Name: "CRM",
		Payload: templatePayload(t, "database", "CRM/schema.json", "CRM/rows/Lead.md"),
	})
	assert.Equal(t, domain.TemplateStatusPublished, view.Status)
	assert.Equal(t, 2, view.FileCount)
	assert.Empty(t, view.ReviewNote)

	market, err := s.service.Market(s.ctx, domain.TemplateKindDatabase, "crm")
	require.NoError(t, err)
	require.Len(t, market, 1)
	assert.Equal(t, view.Slug, market[0].Slug)
	assert.Equal(t, "editor", market[0].OwnerName)

	market, err = s.service.Market(s.ctx, domain.TemplateKindNote, "")
	require.NoError(t, err)
	assert.Empty(t, market)

	_, err = s.service.Market(s.ctx, "page", "")
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	resolved, err := s.service.Resolve(s.ctx, view.Slug)
	require.NoError(t, err)
	assert.Equal(t, view.ID, resolved.ID)
}

func TestMarketReviewRejectsUnsafePayload(t *testing.T) {
	s := newTemplateSetup(t)
	view := s.create(t, template.CreateInput{
		Audience: domain.TemplateAudienceMarket,
		Payload:  templatePayload(t, "note", "../escape.md"),
	})
	assert.Equal(t, domain.TemplateStatusRejected, view.Status)
	assert.Contains(t, view.ReviewNote, "unsafe file path")

	_, err := s.service.Resolve(s.ctx, view.Slug)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	updated, err := s.service.ReplacePayload(s.ctx, template.PayloadInput{
		UserID: s.editor(), VaultID: s.vaultID, TemplateID: view.ID, Payload: templatePayload(t, "note", "ok.md"),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Version)
	assert.Equal(t, domain.TemplateStatusPublished, updated.Status)
	assert.Empty(t, updated.ReviewNote)
}

func TestReviewRules(t *testing.T) {
	s := newTemplateSetup(t)
	tooMany := make([]string, template.MaxFiles+1)
	for i := range tooMany {
		tooMany[i] = "f" + strings.Repeat("x", i%5) + uuid.NewString() + ".md"
	}
	cases := map[string][]byte{
		"absolute":   templatePayload(t, "note", "/etc/passwd.md"),
		"backslash":  templatePayload(t, "note", `a\b.md`),
		"extension":  templatePayload(t, "note", "run.sh"),
		"drive":      templatePayload(t, "note", "C:/a.md"),
		"empty seg":  templatePayload(t, "note", "a//b.md"),
		"duplicate":  templatePayload(t, "note", "a.md", "a.md"),
		"kind":       templatePayload(t, "database", "a.md"),
		"no files":   templatePayload(t, "note"),
		"too many":   templatePayload(t, "note", tooMany...),
		"not json":   []byte("hello"),
		"format":     []byte(`{"format":2,"kind":"note","files":[{"path":"a.md","content":""}]}`),
		"unknown":    []byte(`{"format":1,"kind":"note","files":[{"path":"a.md","content":"","exec":true}]}`),
		"no content": []byte(`{"format":1,"kind":"note","files":[{"path":"a.md"}]}`),
	}
	for name, payload := range cases {
		view := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceMarket, Name: name, Payload: payload})
		assert.Equal(t, domain.TemplateStatusRejected, view.Status, name)
		assert.NotEmpty(t, view.ReviewNote, name)
	}
}

func TestReviewPendingProcessesQueuedTemplates(t *testing.T) {
	s := newTemplateSetup(t)
	owner := s.editor()
	pending := &domain.TemplateShare{
		ID: uuid.New(), VaultID: s.vaultID, OwnerID: &owner, Kind: domain.TemplateKindNote,
		Audience: domain.TemplateAudienceMarket, Name: "Queued", Payload: templatePayload(t, "note", "a.md"),
		Version: 1, Status: domain.TemplateStatusPendingReview, Slug: "queued-slug1",
	}
	require.NoError(t, s.templates.Create(s.ctx, pending))

	count, err := s.service.ReviewPending(s.ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	stored, err := s.templates.Get(s.ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TemplateStatusPublished, stored.Status)
	assert.Equal(t, 1, stored.FileCount)

	count, err = s.service.ReviewPending(s.ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestResolveNeverExposesTeamTemplates(t *testing.T) {
	s := newTemplateSetup(t)
	team := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})
	_, err := s.service.Resolve(s.ctx, team.Slug)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	link := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceLink})
	assert.Equal(t, 1, link.FileCount)
	resolved, err := s.service.Resolve(s.ctx, link.Slug)
	require.NoError(t, err)
	assert.Equal(t, link.ID, resolved.ID)

	market, err := s.service.Market(s.ctx, "", "")
	require.NoError(t, err)
	assert.Empty(t, market)
}

func TestPublicLinksDisabled(t *testing.T) {
	s := newTemplateSetup(t)
	link := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceLink})
	require.NoError(t, s.vaults.Update(s.ctx, &domain.Vault{ID: s.vaultID, Name: "Team", AllowPublicLinks: false}))

	_, err := s.service.Resolve(s.ctx, link.Slug)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = s.service.Create(s.ctx, template.CreateInput{
		UserID: s.editor(), VaultID: s.vaultID, Kind: domain.TemplateKindNote, Audience: domain.TemplateAudienceLink,
		Name: "x", Payload: templatePayload(t, "note", "a.md"),
	})
	assert.ErrorIs(t, err, domain.ErrPublicLinksDisabled)

	s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})
}

func TestUpdatePermissionsAndFields(t *testing.T) {
	s := newTemplateSetup(t)
	design := s.group(t, "Design", s.editor())
	view := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})

	name := "Renamed"
	_, err := s.service.Update(s.ctx, template.UpdateInput{UserID: s.editor2, VaultID: s.vaultID, TemplateID: view.ID, Name: &name})
	assert.ErrorIs(t, err, domain.ErrInsufficientRole)

	samples := true
	updated, err := s.service.Update(s.ctx, template.UpdateInput{
		UserID: s.editor(), VaultID: s.vaultID, TemplateID: view.ID,
		Name: &name, IncludeSamples: &samples, GroupSet: true, GroupID: &design,
	})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", updated.Name)
	assert.True(t, updated.IncludeSamples)
	require.NotNil(t, updated.GroupID)
	assert.Equal(t, design, *updated.GroupID)
	assert.Equal(t, 1, updated.Version)

	assert.NotContains(t, visibleIDs(t, s, s.editor2), view.ID)

	cleared, err := s.service.Update(s.ctx, template.UpdateInput{
		UserID: s.users[domain.VaultRoleAdmin], VaultID: s.vaultID, TemplateID: view.ID, GroupSet: true,
	})
	require.NoError(t, err)
	assert.Nil(t, cleared.GroupID)
	assert.Contains(t, visibleIDs(t, s, s.editor2), view.ID)
}

func TestUpdateAudienceSwitchNeedsMatchingPayload(t *testing.T) {
	s := newTemplateSetup(t)
	view := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})
	market := domain.TemplateAudienceMarket

	_, err := s.service.Update(s.ctx, template.UpdateInput{UserID: s.editor(), VaultID: s.vaultID, TemplateID: view.ID, Audience: &market})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	payload := templatePayload(t, "note", "a.md")
	encrypted := false
	_, err = s.service.Update(s.ctx, template.UpdateInput{UserID: s.editor(), VaultID: s.vaultID, TemplateID: view.ID, Audience: &market, Payload: &payload})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	updated, err := s.service.Update(s.ctx, template.UpdateInput{
		UserID: s.editor(), VaultID: s.vaultID, TemplateID: view.ID, Audience: &market, Payload: &payload, Encrypted: &encrypted,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TemplateAudienceMarket, updated.Audience)
	assert.Equal(t, domain.TemplateStatusPublished, updated.Status)
	assert.Equal(t, 2, updated.Version)
	assert.False(t, updated.Encrypted)
}

func TestDeletePermissions(t *testing.T) {
	s := newTemplateSetup(t)
	view := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceTeam})

	assert.ErrorIs(t, s.service.Delete(s.ctx, s.editor2, s.vaultID, view.ID), domain.ErrInsufficientRole)
	assert.ErrorIs(t, s.service.Delete(s.ctx, s.editor(), s.vaultID, uuid.New()), domain.ErrNotFound)
	require.NoError(t, s.service.Delete(s.ctx, s.users[domain.VaultRoleAdmin], s.vaultID, view.ID))
	assert.Empty(t, visibleIDs(t, s, s.editor()))

	own := s.create(t, template.CreateInput{Audience: domain.TemplateAudienceLink})
	require.NoError(t, s.service.Delete(s.ctx, s.editor(), s.vaultID, own.ID))
}
