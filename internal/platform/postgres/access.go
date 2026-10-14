package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (r *VaultMemberRepository) ListRuleAccess(ctx context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error) {
	rows, err := r.q(ctx).ListUserRuleAccess(ctx, db.ListUserRuleAccessParams{VaultID: vaultID, UserID: userID})
	if err != nil {
		return nil, mapError(err)
	}
	result := make([]domain.RuleAccess, len(rows))
	for i, row := range rows {
		entry := domain.AccessNone
		if row.HasEntry {
			entry = domain.AccessView
			if row.CanEdit {
				entry = domain.AccessEdit
			}
		}
		result[i] = domain.RuleAccess{Path: row.Path, Restricted: row.Restricted, Entry: entry}
	}
	return result, nil
}

type VaultAccessRepository struct{ store }

func NewVaultAccessRepository(pool *pgxpool.Pool) *VaultAccessRepository {
	return &VaultAccessRepository{store{pool}}
}

func (r *VaultAccessRepository) ListGroups(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultGroup, error) {
	rows, err := r.q(ctx).ListGroups(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	groups := make([]domain.VaultGroup, len(rows))
	for i, row := range rows {
		groups[i] = domain.VaultGroup{
			ID: row.ID, VaultID: row.VaultID, Name: row.Name, MemberIDs: row.MemberIds,
			CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return groups, nil
}

func (r *VaultAccessRepository) GetGroup(ctx context.Context, vaultID, groupID uuid.UUID) (*domain.VaultGroup, error) {
	g, err := r.q(ctx).GetGroup(ctx, db.GetGroupParams{VaultID: vaultID, ID: groupID})
	if err != nil {
		return nil, mapError(err)
	}
	return &domain.VaultGroup{ID: g.ID, VaultID: g.VaultID, Name: g.Name, CreatedBy: g.CreatedBy, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}, nil
}

func (r *VaultAccessRepository) CreateGroup(ctx context.Context, group *domain.VaultGroup) error {
	q := r.q(ctx)
	if err := q.CreateGroup(ctx, db.CreateGroupParams{
		ID: group.ID, VaultID: group.VaultID, Name: group.Name, CreatedBy: group.CreatedBy,
		CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
	}); err != nil {
		return mapError(err)
	}
	if len(group.MemberIDs) == 0 {
		return nil
	}
	return mapError(q.AddGroupMembers(ctx, db.AddGroupMembersParams{GroupID: group.ID, UserIds: group.MemberIDs, VaultID: group.VaultID}))
}

func (r *VaultAccessRepository) RenameGroup(ctx context.Context, vaultID, groupID uuid.UUID, name string) error {
	return expectRows(r.q(ctx).RenameGroup(ctx, db.RenameGroupParams{VaultID: vaultID, ID: groupID, Name: name}))
}

func (r *VaultAccessRepository) SetGroupMembers(ctx context.Context, vaultID, groupID uuid.UUID, memberIDs []uuid.UUID) error {
	q := r.q(ctx)
	if err := expectRows(q.TouchGroup(ctx, db.TouchGroupParams{VaultID: vaultID, ID: groupID})); err != nil {
		return err
	}
	if err := q.DeleteGroupMembers(ctx, groupID); err != nil {
		return mapError(err)
	}
	if len(memberIDs) == 0 {
		return nil
	}
	return mapError(q.AddGroupMembers(ctx, db.AddGroupMembersParams{GroupID: groupID, UserIds: memberIDs, VaultID: vaultID}))
}

func (r *VaultAccessRepository) DeleteGroup(ctx context.Context, vaultID, groupID uuid.UUID) error {
	q := r.q(ctx)
	if err := expectRows(q.DeleteGroup(ctx, db.DeleteGroupParams{VaultID: vaultID, ID: groupID})); err != nil {
		return err
	}
	if err := q.DeletePrincipalEntries(ctx, db.DeletePrincipalEntriesParams{
		VaultID: vaultID, PrincipalType: string(domain.PrincipalGroup), PrincipalID: groupID,
	}); err != nil {
		return mapError(err)
	}
	return mapError(q.DeleteOrphanRules(ctx, vaultID))
}

func (r *VaultAccessRepository) ListRules(ctx context.Context, vaultID uuid.UUID) ([]domain.PathRule, error) {
	q := r.q(ctx)
	rows, err := q.ListRules(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	entries, err := q.ListEntries(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	byPath := make(map[string][]domain.AccessEntry, len(rows))
	for _, e := range entries {
		level, _ := domain.ParseAccessLevel(e.Level)
		byPath[e.Path] = append(byPath[e.Path], domain.AccessEntry{
			PrincipalType: domain.PrincipalType(e.PrincipalType), PrincipalID: e.PrincipalID, Level: level,
		})
	}
	rules := make([]domain.PathRule, len(rows))
	for i, row := range rows {
		rules[i] = domain.PathRule{
			VaultID: row.VaultID, Path: row.Path, Restricted: row.Restricted, Entries: byPath[row.Path],
			UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt,
		}
	}
	return rules, nil
}

func (r *VaultAccessRepository) PutRule(ctx context.Context, rule *domain.PathRule) error {
	q := r.q(ctx)
	if !rule.Restricted && len(rule.Entries) == 0 {
		_, err := q.DeleteRule(ctx, db.DeleteRuleParams{VaultID: rule.VaultID, Path: rule.Path})
		return mapError(err)
	}
	if err := q.UpsertRule(ctx, db.UpsertRuleParams{
		VaultID: rule.VaultID, Path: rule.Path, Restricted: rule.Restricted, UpdatedBy: rule.UpdatedBy,
	}); err != nil {
		return mapError(err)
	}
	if err := q.DeleteRuleEntries(ctx, db.DeleteRuleEntriesParams{VaultID: rule.VaultID, Path: rule.Path}); err != nil {
		return mapError(err)
	}
	if len(rule.Entries) == 0 {
		return nil
	}
	types := make([]string, len(rule.Entries))
	ids := make([]uuid.UUID, len(rule.Entries))
	levels := make([]string, len(rule.Entries))
	for i, e := range rule.Entries {
		types[i] = string(e.PrincipalType)
		ids[i] = e.PrincipalID
		levels[i] = e.Level.String()
	}
	return mapError(q.InsertEntries(ctx, db.InsertEntriesParams{
		VaultID: rule.VaultID, Path: rule.Path, PrincipalTypes: types, PrincipalIds: ids, Levels: levels,
	}))
}

func (r *VaultAccessRepository) UpsertUserEntry(ctx context.Context, vaultID uuid.UUID, path string, userID uuid.UUID, level domain.AccessLevel, updatedBy uuid.UUID) error {
	q := r.q(ctx)
	if err := q.EnsureRule(ctx, db.EnsureRuleParams{VaultID: vaultID, Path: path, UpdatedBy: &updatedBy}); err != nil {
		return mapError(err)
	}
	return mapError(q.UpsertEntry(ctx, db.UpsertEntryParams{
		VaultID: vaultID, Path: path, PrincipalType: string(domain.PrincipalUser), PrincipalID: userID, Level: level.String(),
	}))
}

func (r *VaultAccessRepository) RemovePrincipal(ctx context.Context, vaultID uuid.UUID, principalType domain.PrincipalType, principalID uuid.UUID) error {
	q := r.q(ctx)
	if principalType == domain.PrincipalUser {
		if err := q.DeleteUserGroupMemberships(ctx, db.DeleteUserGroupMembershipsParams{VaultID: vaultID, UserID: principalID}); err != nil {
			return mapError(err)
		}
	}
	if err := q.DeletePrincipalEntries(ctx, db.DeletePrincipalEntriesParams{
		VaultID: vaultID, PrincipalType: string(principalType), PrincipalID: principalID,
	}); err != nil {
		return mapError(err)
	}
	return mapError(q.DeleteOrphanRules(ctx, vaultID))
}

func (r *VaultAccessRepository) RulesCovering(ctx context.Context, vaultID uuid.UUID, path string) ([]string, error) {
	paths, err := r.q(ctx).ListRulesCovering(ctx, db.ListRulesCoveringParams{VaultID: vaultID, Path: path})
	return paths, mapError(err)
}

func (r *VaultAccessRepository) RuleExists(ctx context.Context, vaultID uuid.UUID, path string) (bool, error) {
	exists, err := r.q(ctx).RuleExists(ctx, db.RuleExistsParams{VaultID: vaultID, Path: path})
	return exists, mapError(err)
}

func (r *VaultAccessRepository) LiveFileExistsUnder(ctx context.Context, vaultID uuid.UUID, prefix, exclude string) (bool, error) {
	exists, err := r.q(ctx).LiveFileExistsUnder(ctx, db.LiveFileExistsUnderParams{
		VaultID: vaultID, Pattern: escapeLike(prefix) + "%", Exclude: exclude,
	})
	return exists, mapError(err)
}

func (r *VaultAccessRepository) CopyRule(ctx context.Context, vaultID uuid.UUID, from, to string) error {
	q := r.q(ctx)
	if err := q.CopyRule(ctx, db.CopyRuleParams{VaultID: vaultID, FromPath: from, ToPath: to}); err != nil {
		return mapError(err)
	}
	return mapError(q.CopyRuleEntries(ctx, db.CopyRuleEntriesParams{VaultID: vaultID, FromPath: from, ToPath: to}))
}

func (r *VaultAccessRepository) MoveRule(ctx context.Context, vaultID uuid.UUID, from, to string) error {
	q := r.q(ctx)
	if _, err := q.DeleteRule(ctx, db.DeleteRuleParams{VaultID: vaultID, Path: to}); err != nil {
		return mapError(err)
	}
	_, err := q.MoveRule(ctx, db.MoveRuleParams{VaultID: vaultID, FromPath: from, ToPath: to})
	return mapError(err)
}

func (r *VaultAccessRepository) DeleteRule(ctx context.Context, vaultID uuid.UUID, path string) (bool, error) {
	n, err := r.q(ctx).DeleteRule(ctx, db.DeleteRuleParams{VaultID: vaultID, Path: path})
	if err != nil {
		return false, mapError(err)
	}
	return n > 0, nil
}
