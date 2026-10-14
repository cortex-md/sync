package template

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/secret"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	maxNameRunes        = 120
	maxDescriptionRunes = 2000
	slugBytes           = 9
	slugAttempts        = 3
	MarketLimit         = 100
	pendingBatchSize    = 50
)

type Service struct {
	templates TemplateRepository
	groups    GroupLookup
	users     UserLookup
	vaults    VaultLookup
	tx        Transactor
	authz     *authz.Authorizer
}

type Deps struct {
	Templates TemplateRepository
	Groups    GroupLookup
	Users     UserLookup
	Vaults    VaultLookup
	Tx        Transactor
	Authz     *authz.Authorizer
}

func NewService(d Deps) *Service {
	return &Service{
		templates: d.Templates,
		groups:    d.Groups,
		users:     d.Users,
		vaults:    d.Vaults,
		tx:        d.Tx,
		authz:     d.Authz,
	}
}

type View struct {
	domain.TemplateShare
	OwnerName string
	GroupName *string
}

type caller struct {
	userID   uuid.UUID
	member   domain.VaultMember
	groupIDs map[uuid.UUID]struct{}
	groups   map[uuid.UUID]string
}

func (c *caller) admin() bool {
	return c.member.Role.Can(domain.PermVaultUpdate)
}

func (c *caller) canSee(t *domain.TemplateShare) bool {
	if t.OwnedBy(c.userID) || c.admin() {
		return true
	}
	if t.Audience != domain.TemplateAudienceTeam {
		return false
	}
	if t.GroupID == nil {
		return true
	}
	_, ok := c.groupIDs[*t.GroupID]
	return ok
}

func (c *caller) canEdit(t *domain.TemplateShare) bool {
	return c.admin() || (t.OwnedBy(c.userID) && c.member.Role.Can(domain.PermNoteShare))
}

func (c *caller) canDelete(t *domain.TemplateShare) bool {
	return c.admin() || t.OwnedBy(c.userID)
}

func (s *Service) caller(ctx context.Context, userID, vaultID uuid.UUID, permission domain.Permission) (*caller, error) {
	member, err := s.authz.Require(ctx, vaultID, userID, permission)
	if err != nil {
		return nil, err
	}
	groups, err := s.groups.ListGroups(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	c := &caller{
		userID:   userID,
		member:   *member,
		groupIDs: make(map[uuid.UUID]struct{}),
		groups:   make(map[uuid.UUID]string, len(groups)),
	}
	for _, group := range groups {
		c.groups[group.ID] = group.Name
		for _, memberID := range group.MemberIDs {
			if memberID == userID {
				c.groupIDs[group.ID] = struct{}{}
				break
			}
		}
	}
	return c, nil
}

const readPermission = domain.PermFileRead | domain.PermMemberRead

func (s *Service) List(ctx context.Context, userID, vaultID uuid.UUID) ([]View, error) {
	c, err := s.caller(ctx, userID, vaultID, readPermission)
	if err != nil {
		return nil, err
	}
	templates, err := s.templates.ListByVault(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	names := newNameCache(s.users)
	views := make([]View, 0, len(templates))
	for i := range templates {
		if c.canSee(&templates[i]) {
			views = append(views, s.view(ctx, names, c.groups, templates[i]))
		}
	}
	return views, nil
}

type CreateInput struct {
	UserID         uuid.UUID
	VaultID        uuid.UUID
	Kind           domain.TemplateKind
	Audience       domain.TemplateAudience
	GroupID        *uuid.UUID
	Name           string
	Description    string
	IncludeSamples bool
	Payload        []byte
	Encrypted      bool
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*View, error) {
	if !input.Kind.Valid() || !input.Audience.Valid() {
		return nil, domain.ErrInvalidInput
	}
	name, description, err := normalizeText(input.Name, input.Description)
	if err != nil {
		return nil, err
	}
	if err := validatePayload(input.Audience, input.Payload, input.Encrypted); err != nil {
		return nil, err
	}
	c, err := s.caller(ctx, input.UserID, input.VaultID, domain.PermNoteShare)
	if err != nil {
		return nil, err
	}
	if err := s.checkAudience(ctx, c, input.VaultID, input.Audience, input.GroupID); err != nil {
		return nil, err
	}

	owner := input.UserID
	t := &domain.TemplateShare{
		ID:             uuid.New(),
		VaultID:        input.VaultID,
		OwnerID:        &owner,
		Kind:           input.Kind,
		Audience:       input.Audience,
		GroupID:        groupFor(input.Audience, input.GroupID),
		Name:           name,
		Description:    description,
		IncludeSamples: input.IncludeSamples,
		Payload:        input.Payload,
		Encrypted:      input.Encrypted,
		Version:        1,
	}
	applyPayloadState(t)
	if err := s.insert(ctx, t); err != nil {
		return nil, err
	}
	s.reviewIfPending(ctx, t)
	view := s.view(ctx, newNameCache(s.users), c.groups, *t)
	return &view, nil
}

type Payload struct {
	Data      []byte
	Encrypted bool
	Version   int
}

func (s *Service) GetPayload(ctx context.Context, userID, vaultID, templateID uuid.UUID) (*Payload, error) {
	c, err := s.caller(ctx, userID, vaultID, readPermission)
	if err != nil {
		return nil, err
	}
	t, err := s.vaultTemplate(ctx, vaultID, templateID)
	if err != nil {
		return nil, err
	}
	if !c.canSee(t) {
		return nil, domain.ErrNotFound
	}
	return &Payload{Data: t.Payload, Encrypted: t.Encrypted, Version: t.Version}, nil
}

type PayloadInput struct {
	UserID     uuid.UUID
	VaultID    uuid.UUID
	TemplateID uuid.UUID
	Payload    []byte
	Encrypted  bool
}

func (s *Service) ReplacePayload(ctx context.Context, input PayloadInput) (*View, error) {
	payload := input.Payload
	encrypted := input.Encrypted
	return s.Update(ctx, UpdateInput{
		UserID:     input.UserID,
		VaultID:    input.VaultID,
		TemplateID: input.TemplateID,
		Payload:    &payload,
		Encrypted:  &encrypted,
	})
}

type UpdateInput struct {
	UserID         uuid.UUID
	VaultID        uuid.UUID
	TemplateID     uuid.UUID
	Audience       *domain.TemplateAudience
	GroupSet       bool
	GroupID        *uuid.UUID
	Name           *string
	Description    *string
	IncludeSamples *bool
	Payload        *[]byte
	Encrypted      *bool
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (*View, error) {
	if (input.Payload == nil) != (input.Encrypted == nil) {
		return nil, domain.ErrInvalidInput
	}
	if input.Audience != nil && !input.Audience.Valid() {
		return nil, domain.ErrInvalidInput
	}
	c, err := s.caller(ctx, input.UserID, input.VaultID, readPermission)
	if err != nil {
		return nil, err
	}

	var updated *domain.TemplateShare
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		t, err := s.templates.GetForUpdate(ctx, input.TemplateID)
		if err != nil {
			return err
		}
		if t.VaultID != input.VaultID || !c.canSee(t) {
			return domain.ErrNotFound
		}
		if !c.canEdit(t) {
			return domain.ErrInsufficientRole
		}
		if err := s.applyUpdate(ctx, c, t, input); err != nil {
			return err
		}
		if err := s.templates.Update(ctx, t); err != nil {
			return err
		}
		updated = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.reviewIfPending(ctx, updated)
	view := s.view(ctx, newNameCache(s.users), c.groups, *updated)
	return &view, nil
}

func (s *Service) applyUpdate(ctx context.Context, c *caller, t *domain.TemplateShare, input UpdateInput) error {
	name, description := t.Name, t.Description
	if input.Name != nil {
		name = *input.Name
	}
	if input.Description != nil {
		description = *input.Description
	}
	name, description, err := normalizeText(name, description)
	if err != nil {
		return err
	}

	audience := t.Audience
	if input.Audience != nil {
		audience = *input.Audience
	}
	groupID := t.GroupID
	if input.GroupSet {
		groupID = input.GroupID
	}
	if audience != domain.TemplateAudienceTeam {
		if input.GroupSet && input.GroupID != nil {
			return domain.ErrInvalidInput
		}
		groupID = nil
	}
	audienceChanged := audience != t.Audience
	groupChanged := !sameGroup(groupID, t.GroupID)
	if audienceChanged || groupChanged {
		if err := s.checkAudience(ctx, c, t.VaultID, audience, groupID); err != nil {
			return err
		}
	}

	payload, encrypted := t.Payload, t.Encrypted
	payloadChanged := input.Payload != nil
	if payloadChanged {
		payload, encrypted = *input.Payload, *input.Encrypted
	}
	if err := validatePayload(audience, payload, encrypted); err != nil {
		return err
	}

	t.Name, t.Description = name, description
	t.Audience, t.GroupID = audience, groupID
	if input.IncludeSamples != nil {
		t.IncludeSamples = *input.IncludeSamples
	}
	if payloadChanged {
		t.Payload, t.Encrypted = payload, encrypted
		t.Version++
	}
	if payloadChanged || audienceChanged {
		applyPayloadState(t)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, userID, vaultID, templateID uuid.UUID) error {
	c, err := s.caller(ctx, userID, vaultID, readPermission)
	if err != nil {
		return err
	}
	t, err := s.vaultTemplate(ctx, vaultID, templateID)
	if err != nil {
		return err
	}
	if !c.canSee(t) {
		return domain.ErrNotFound
	}
	if !c.canDelete(t) {
		return domain.ErrInsufficientRole
	}
	return s.templates.Delete(ctx, t.ID)
}

type PublicView struct {
	domain.TemplateShare
	OwnerName string
}

func (s *Service) Market(ctx context.Context, kind domain.TemplateKind, query string) ([]PublicView, error) {
	if kind != "" && !kind.Valid() {
		return nil, domain.ErrInvalidInput
	}
	templates, err := s.templates.ListMarket(ctx, kind, strings.TrimSpace(query), MarketLimit)
	if err != nil {
		return nil, err
	}
	names := newNameCache(s.users)
	allowed := make(map[uuid.UUID]bool)
	views := make([]PublicView, 0, len(templates))
	for _, t := range templates {
		ok, seen := allowed[t.VaultID]
		if !seen {
			ok = s.publicLinksAllowed(ctx, t.VaultID)
			allowed[t.VaultID] = ok
		}
		if ok {
			views = append(views, PublicView{TemplateShare: t, OwnerName: names.name(ctx, t.OwnerID)})
		}
	}
	return views, nil
}

func (s *Service) Resolve(ctx context.Context, slug string) (*PublicView, error) {
	if slug == "" {
		return nil, domain.ErrNotFound
	}
	t, err := s.templates.GetBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !t.PubliclyAvailable() || !s.publicLinksAllowed(ctx, t.VaultID) {
		return nil, domain.ErrNotFound
	}
	return &PublicView{TemplateShare: *t, OwnerName: newNameCache(s.users).name(ctx, t.OwnerID)}, nil
}

func (s *Service) ReviewPending(ctx context.Context) (int64, error) {
	pending, err := s.templates.ListPending(ctx, pendingBatchSize)
	if err != nil {
		return 0, err
	}
	var reviewed int64
	for i := range pending {
		changed, err := s.review(ctx, &pending[i])
		if err != nil {
			return reviewed, err
		}
		if changed {
			reviewed++
		}
	}
	return reviewed, nil
}

func (s *Service) reviewIfPending(ctx context.Context, t *domain.TemplateShare) {
	if t.Status != domain.TemplateStatusPendingReview {
		return
	}
	if _, err := s.review(context.WithoutCancel(ctx), t); err != nil {
		log.Warn().Err(err).Str("template_id", t.ID.String()).Msg("template: review deferred to background job")
	}
}

func (s *Service) review(ctx context.Context, t *domain.TemplateShare) (bool, error) {
	result := reviewPayload(t.Kind, t.Payload)
	status := domain.TemplateStatusPublished
	if !result.Approved() {
		status = domain.TemplateStatusRejected
	}
	changed, err := s.templates.SetReview(ctx, t.ID, t.Version, status, result.Note, result.FileCount)
	if err != nil {
		return false, err
	}
	if changed {
		t.Status, t.ReviewNote, t.FileCount = status, result.Note, result.FileCount
	}
	return changed, nil
}

func (s *Service) insert(ctx context.Context, t *domain.TemplateShare) error {
	for attempt := 0; attempt < slugAttempts; attempt++ {
		slug, err := secret.Slug(slugBytes)
		if err != nil {
			return err
		}
		t.Slug = slug
		err = s.templates.Create(ctx, t)
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return err
		}
	}
	return domain.ErrConflict
}

func (s *Service) checkAudience(ctx context.Context, c *caller, vaultID uuid.UUID, audience domain.TemplateAudience, groupID *uuid.UUID) error {
	if audience == domain.TemplateAudienceTeam {
		if groupID == nil {
			return nil
		}
		if _, ok := c.groups[*groupID]; !ok {
			return domain.ErrInvalidInput
		}
		return nil
	}
	if groupID != nil {
		return domain.ErrInvalidInput
	}
	vault, err := s.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return err
	}
	if !vault.AllowPublicLinks {
		return domain.ErrPublicLinksDisabled
	}
	return nil
}

func (s *Service) publicLinksAllowed(ctx context.Context, vaultID uuid.UUID) bool {
	vault, err := s.vaults.GetByID(ctx, vaultID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			log.Warn().Err(err).Str("vault_id", vaultID.String()).Msg("template: vault lookup failed")
		}
		return false
	}
	return vault.AllowPublicLinks
}

func (s *Service) vaultTemplate(ctx context.Context, vaultID, templateID uuid.UUID) (*domain.TemplateShare, error) {
	t, err := s.templates.Get(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if t.VaultID != vaultID {
		return nil, domain.ErrNotFound
	}
	return t, nil
}

func (s *Service) view(ctx context.Context, names *nameCache, groups map[uuid.UUID]string, t domain.TemplateShare) View {
	view := View{TemplateShare: t, OwnerName: names.name(ctx, t.OwnerID)}
	if t.GroupID != nil {
		if name, ok := groups[*t.GroupID]; ok {
			view.GroupName = &name
		}
	}
	return view
}

func applyPayloadState(t *domain.TemplateShare) {
	t.ReviewNote = ""
	switch t.Audience {
	case domain.TemplateAudienceMarket:
		t.Status = domain.TemplateStatusPendingReview
		t.FileCount = 0
	case domain.TemplateAudienceLink:
		t.Status = domain.TemplateStatusPublished
		t.FileCount = countFiles(t.Kind, t.Payload)
	default:
		t.Status = domain.TemplateStatusPublished
		t.FileCount = 0
	}
}

func validatePayload(audience domain.TemplateAudience, payload []byte, encrypted bool) error {
	if len(payload) == 0 {
		return domain.ErrInvalidInput
	}
	if len(payload) > MaxPayloadBytes {
		return domain.ErrFileTooLarge
	}
	if encrypted != (audience == domain.TemplateAudienceTeam) {
		return domain.ErrInvalidInput
	}
	return nil
}

func normalizeText(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	nameRunes := utf8.RuneCountInString(name)
	if nameRunes == 0 || nameRunes > maxNameRunes || utf8.RuneCountInString(description) > maxDescriptionRunes {
		return "", "", domain.ErrInvalidInput
	}
	return name, description, nil
}

func groupFor(audience domain.TemplateAudience, groupID *uuid.UUID) *uuid.UUID {
	if audience != domain.TemplateAudienceTeam {
		return nil
	}
	return groupID
}

func sameGroup(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type nameCache struct {
	users UserLookup
	names map[uuid.UUID]string
}

func newNameCache(users UserLookup) *nameCache {
	return &nameCache{users: users, names: make(map[uuid.UUID]string)}
}

func (n *nameCache) name(ctx context.Context, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	if name, ok := n.names[*id]; ok {
		return name
	}
	user, err := n.users.GetByID(ctx, *id)
	name := ""
	if err == nil {
		name = user.DisplayName
	}
	n.names[*id] = name
	return name
}
