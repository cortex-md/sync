package vault

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/google/uuid"
)

const (
	maxGroupNameRunes = 64
	maxRuleEntries    = 500
)

type AccessService struct {
	access  AccessRepository
	members VaultMemberRepository
	tx      Transactor
	authz   *authz.Authorizer
	bus     bus.Bus
}

type AccessDeps struct {
	Access  AccessRepository
	Members VaultMemberRepository
	Tx      Transactor
	Authz   *authz.Authorizer
	Bus     bus.Bus
}

func NewAccessService(d AccessDeps) *AccessService {
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &AccessService{access: d.Access, members: d.Members, tx: d.Tx, authz: authorizer, bus: d.Bus}
}

func (s *AccessService) Changed(ctx context.Context, vaultID uuid.UUID) {
	s.authz.InvalidateVault(vaultID)
	bus.PublishVaultAccessChanged(context.WithoutCancel(ctx), s.bus, vaultID)
}

func (s *AccessService) ListGroups(ctx context.Context, actorID, vaultID uuid.UUID) ([]domain.VaultGroup, error) {
	if _, err := s.authz.Require(ctx, vaultID, actorID, domain.PermMemberRead); err != nil {
		return nil, err
	}
	return s.access.ListGroups(ctx, vaultID)
}

type CreateGroupInput struct {
	ActorID   uuid.UUID
	VaultID   uuid.UUID
	Name      string
	MemberIDs []uuid.UUID
}

func (s *AccessService) CreateGroup(ctx context.Context, input CreateGroupInput) (*domain.VaultGroup, error) {
	name, ok := normalizeGroupName(input.Name)
	if !ok {
		return nil, domain.ErrInvalidInput
	}
	if _, err := s.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermMemberManage); err != nil {
		return nil, err
	}
	memberIDs, err := s.validMembers(ctx, input.VaultID, input.MemberIDs)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	actor := input.ActorID
	group := &domain.VaultGroup{
		ID:        uuid.New(),
		VaultID:   input.VaultID,
		Name:      name,
		MemberIDs: memberIDs,
		CreatedBy: &actor,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := runInTx(ctx, s.tx, func(ctx context.Context) error {
		return groupNameError(s.access.CreateGroup(ctx, group))
	}); err != nil {
		return nil, err
	}
	s.Changed(ctx, input.VaultID)
	return group, nil
}

type UpdateGroupInput struct {
	ActorID   uuid.UUID
	VaultID   uuid.UUID
	GroupID   uuid.UUID
	Name      *string
	MemberIDs *[]uuid.UUID
}

func (s *AccessService) UpdateGroup(ctx context.Context, input UpdateGroupInput) (*domain.VaultGroup, error) {
	var name string
	if input.Name != nil {
		normalized, ok := normalizeGroupName(*input.Name)
		if !ok {
			return nil, domain.ErrInvalidInput
		}
		name = normalized
	}
	if _, err := s.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermMemberManage); err != nil {
		return nil, err
	}
	if _, err := s.access.GetGroup(ctx, input.VaultID, input.GroupID); err != nil {
		return nil, err
	}
	var memberIDs []uuid.UUID
	if input.MemberIDs != nil {
		ids, err := s.validMembers(ctx, input.VaultID, *input.MemberIDs)
		if err != nil {
			return nil, err
		}
		memberIDs = ids
	}
	if err := runInTx(ctx, s.tx, func(ctx context.Context) error {
		if input.Name != nil {
			if err := groupNameError(s.access.RenameGroup(ctx, input.VaultID, input.GroupID, name)); err != nil {
				return err
			}
		}
		if input.MemberIDs != nil {
			return s.access.SetGroupMembers(ctx, input.VaultID, input.GroupID, memberIDs)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	s.Changed(ctx, input.VaultID)
	return s.group(ctx, input.VaultID, input.GroupID)
}

func (s *AccessService) DeleteGroup(ctx context.Context, actorID, vaultID, groupID uuid.UUID) error {
	if _, err := s.authz.Require(ctx, vaultID, actorID, domain.PermMemberManage); err != nil {
		return err
	}
	if err := runInTx(ctx, s.tx, func(ctx context.Context) error {
		return s.access.DeleteGroup(ctx, vaultID, groupID)
	}); err != nil {
		return err
	}
	s.Changed(ctx, vaultID)
	return nil
}

func (s *AccessService) group(ctx context.Context, vaultID, groupID uuid.UUID) (*domain.VaultGroup, error) {
	groups, err := s.access.ListGroups(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].ID == groupID {
			return &groups[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *AccessService) validMembers(ctx context.Context, vaultID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	members, err := s.members.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	known := make(map[uuid.UUID]bool, len(members))
	for _, m := range members {
		known[m.UserID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return nil, domain.ErrInvalidInput
		}
		if !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return result, nil
}

func runInTx(ctx context.Context, tx Transactor, fn func(ctx context.Context) error) error {
	if tx == nil {
		return fn(ctx)
	}
	return tx.RunInTx(ctx, fn)
}

func normalizeGroupName(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	n := utf8.RuneCountInString(trimmed)
	return trimmed, n >= 1 && n <= maxGroupNameRunes
}

func groupNameError(err error) error {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return domain.ErrGroupNameTaken
	}
	return err
}

func (s *AccessService) ListRules(ctx context.Context, actorID, vaultID uuid.UUID) ([]domain.PathRule, error) {
	if _, err := s.authz.Require(ctx, vaultID, actorID, domain.PermMemberManage); err != nil {
		return nil, err
	}
	return s.access.ListRules(ctx, vaultID)
}

type AccessEntryInput struct {
	PrincipalType string
	PrincipalID   uuid.UUID
	Level         string
}

type PutRuleInput struct {
	ActorID    uuid.UUID
	VaultID    uuid.UUID
	Path       string
	Restricted bool
	Entries    []AccessEntryInput
}

func (s *AccessService) PutRule(ctx context.Context, input PutRuleInput) (*domain.PathRule, error) {
	path, ok := domain.NormalizeGrantPath(input.Path)
	if !ok || len(input.Entries) > maxRuleEntries {
		return nil, domain.ErrInvalidInput
	}
	if _, err := s.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermMemberManage); err != nil {
		return nil, err
	}
	entries, err := s.validEntries(ctx, input.VaultID, input.Entries)
	if err != nil {
		return nil, err
	}
	actor := input.ActorID
	rule := &domain.PathRule{
		VaultID:    input.VaultID,
		Path:       path,
		Restricted: input.Restricted,
		Entries:    entries,
		UpdatedBy:  &actor,
		UpdatedAt:  time.Now().UTC(),
	}
	if err := runInTx(ctx, s.tx, func(ctx context.Context) error {
		return s.access.PutRule(ctx, rule)
	}); err != nil {
		return nil, err
	}
	s.Changed(ctx, input.VaultID)
	if !rule.Restricted && len(rule.Entries) == 0 {
		return nil, nil
	}
	return rule, nil
}

func (s *AccessService) validEntries(ctx context.Context, vaultID uuid.UUID, inputs []AccessEntryInput) ([]domain.AccessEntry, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	members, err := s.members.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	users := make(map[uuid.UUID]bool, len(members))
	for _, m := range members {
		users[m.UserID] = true
	}
	var groups map[uuid.UUID]bool
	entries := make([]domain.AccessEntry, 0, len(inputs))
	index := make(map[domain.AccessEntry]int, len(inputs))
	for _, in := range inputs {
		level, ok := domain.ParseAccessLevel(in.Level)
		if !ok {
			return nil, domain.ErrInvalidInput
		}
		principal := domain.PrincipalType(in.PrincipalType)
		switch principal {
		case domain.PrincipalUser:
			if !users[in.PrincipalID] {
				return nil, domain.ErrInvalidInput
			}
		case domain.PrincipalGroup:
			if groups == nil {
				list, err := s.access.ListGroups(ctx, vaultID)
				if err != nil {
					return nil, err
				}
				groups = make(map[uuid.UUID]bool, len(list))
				for _, g := range list {
					groups[g.ID] = true
				}
			}
			if !groups[in.PrincipalID] {
				return nil, domain.ErrInvalidInput
			}
		default:
			return nil, domain.ErrInvalidInput
		}
		key := domain.AccessEntry{PrincipalType: principal, PrincipalID: in.PrincipalID}
		if i, seen := index[key]; seen {
			entries[i].Level = max(entries[i].Level, level)
			continue
		}
		index[key] = len(entries)
		entries = append(entries, domain.AccessEntry{PrincipalType: principal, PrincipalID: in.PrincipalID, Level: level})
	}
	return entries, nil
}

func (s *AccessService) MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) (bool, error) {
	covering, err := s.access.RulesCovering(ctx, vaultID, oldPath)
	if err != nil || len(covering) == 0 {
		return false, err
	}
	changed := false
	folders := make([]string, 0, len(covering))
	for _, path := range covering {
		if path == oldPath {
			if err := s.access.MoveRule(ctx, vaultID, oldPath, newPath); err != nil {
				return false, err
			}
			changed = true
			continue
		}
		folders = append(folders, path)
	}
	for _, move := range domain.FolderCarryCandidates(oldPath, newPath, folders) {
		exists, err := s.access.RuleExists(ctx, vaultID, move.To)
		if err != nil {
			return false, err
		}
		if exists {
			continue
		}
		occupied, err := s.access.LiveFileExistsUnder(ctx, vaultID, move.To, newPath)
		if err != nil {
			return false, err
		}
		if occupied {
			continue
		}
		if err := s.access.CopyRule(ctx, vaultID, move.From, move.To); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func (s *AccessService) RemoveForPath(ctx context.Context, vaultID uuid.UUID, path string) (bool, error) {
	if domain.IsFolderRule(path) {
		return false, nil
	}
	return s.access.DeleteRule(ctx, vaultID, path)
}
