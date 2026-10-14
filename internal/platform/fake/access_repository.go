package fake

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

func (r *VaultMemberRepository) ResolveFilesWith(files *FileLatestRepository) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files = files
}

func (r *VaultMemberRepository) ListRuleAccess(_ context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	myGroups := make(map[uuid.UUID]bool)
	for _, g := range r.groups {
		if g.VaultID != vaultID {
			continue
		}
		for _, id := range g.MemberIDs {
			if id == userID {
				myGroups[g.ID] = true
			}
		}
	}
	var result []domain.RuleAccess
	for _, rule := range r.rules[vaultID] {
		entry := domain.AccessNone
		has := false
		for _, e := range rule.Entries {
			mine := (e.PrincipalType == domain.PrincipalUser && e.PrincipalID == userID) ||
				(e.PrincipalType == domain.PrincipalGroup && myGroups[e.PrincipalID])
			if mine {
				has = true
				if e.Level > entry {
					entry = e.Level
				}
			}
		}
		if rule.Restricted || has {
			result = append(result, domain.RuleAccess{Path: rule.Path, Restricted: rule.Restricted, Entry: entry})
		}
	}
	return result, nil
}

func copyGroup(g *domain.VaultGroup) domain.VaultGroup {
	out := *g
	out.MemberIDs = append([]uuid.UUID(nil), g.MemberIDs...)
	return out
}

func (r *VaultMemberRepository) ListGroups(_ context.Context, vaultID uuid.UUID) ([]domain.VaultGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VaultGroup
	for _, g := range r.groups {
		if g.VaultID == vaultID {
			result = append(result, copyGroup(g))
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result, nil
}

func (r *VaultMemberRepository) GetGroup(_ context.Context, vaultID, groupID uuid.UUID) (*domain.VaultGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.groups[groupID]
	if !ok || g.VaultID != vaultID {
		return nil, domain.ErrNotFound
	}
	out := copyGroup(g)
	return &out, nil
}

func (r *VaultMemberRepository) groupNameTaken(vaultID, groupID uuid.UUID, name string) bool {
	for _, g := range r.groups {
		if g.VaultID == vaultID && g.ID != groupID && strings.EqualFold(g.Name, name) {
			return true
		}
	}
	return false
}

func (r *VaultMemberRepository) CreateGroup(_ context.Context, group *domain.VaultGroup) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.groups[group.ID]; exists || r.groupNameTaken(group.VaultID, group.ID, group.Name) {
		return domain.ErrAlreadyExists
	}
	stored := copyGroup(group)
	r.groups[group.ID] = &stored
	return nil
}

func (r *VaultMemberRepository) RenameGroup(_ context.Context, vaultID, groupID uuid.UUID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok || g.VaultID != vaultID {
		return domain.ErrNotFound
	}
	if r.groupNameTaken(vaultID, groupID, name) {
		return domain.ErrAlreadyExists
	}
	g.Name = name
	g.UpdatedAt = time.Now()
	return nil
}

func (r *VaultMemberRepository) SetGroupMembers(_ context.Context, vaultID, groupID uuid.UUID, memberIDs []uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok || g.VaultID != vaultID {
		return domain.ErrNotFound
	}
	g.MemberIDs = append([]uuid.UUID(nil), memberIDs...)
	g.UpdatedAt = time.Now()
	return nil
}

func (r *VaultMemberRepository) DeleteGroup(_ context.Context, vaultID, groupID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok || g.VaultID != vaultID {
		return domain.ErrNotFound
	}
	delete(r.groups, groupID)
	r.removeEntriesLocked(vaultID, domain.PrincipalGroup, groupID)
	return nil
}

func copyRule(rule *domain.PathRule) domain.PathRule {
	out := *rule
	out.Entries = append([]domain.AccessEntry(nil), rule.Entries...)
	return out
}

func (r *VaultMemberRepository) ListRules(_ context.Context, vaultID uuid.UUID) ([]domain.PathRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.PathRule, 0, len(r.rules[vaultID]))
	for _, rule := range r.rules[vaultID] {
		result = append(result, copyRule(rule))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func (r *VaultMemberRepository) vaultRules(vaultID uuid.UUID) map[string]*domain.PathRule {
	rules, ok := r.rules[vaultID]
	if !ok {
		rules = make(map[string]*domain.PathRule)
		r.rules[vaultID] = rules
	}
	return rules
}

func (r *VaultMemberRepository) PutRule(_ context.Context, rule *domain.PathRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rules := r.vaultRules(rule.VaultID)
	if !rule.Restricted && len(rule.Entries) == 0 {
		delete(rules, rule.Path)
		return nil
	}
	stored := copyRule(rule)
	stored.UpdatedAt = time.Now()
	rules[rule.Path] = &stored
	return nil
}

func (r *VaultMemberRepository) UpsertUserEntry(_ context.Context, vaultID uuid.UUID, path string, userID uuid.UUID, level domain.AccessLevel, updatedBy uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rules := r.vaultRules(vaultID)
	rule, ok := rules[path]
	if !ok {
		by := updatedBy
		rule = &domain.PathRule{VaultID: vaultID, Path: path, UpdatedBy: &by, UpdatedAt: time.Now()}
		rules[path] = rule
	}
	for i, e := range rule.Entries {
		if e.PrincipalType == domain.PrincipalUser && e.PrincipalID == userID {
			rule.Entries[i].Level = level
			return nil
		}
	}
	rule.Entries = append(rule.Entries, domain.AccessEntry{PrincipalType: domain.PrincipalUser, PrincipalID: userID, Level: level})
	return nil
}

func (r *VaultMemberRepository) RemovePrincipal(_ context.Context, vaultID uuid.UUID, principalType domain.PrincipalType, principalID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if principalType == domain.PrincipalUser {
		for _, g := range r.groups {
			if g.VaultID != vaultID {
				continue
			}
			kept := g.MemberIDs[:0]
			for _, id := range g.MemberIDs {
				if id != principalID {
					kept = append(kept, id)
				}
			}
			g.MemberIDs = kept
		}
	}
	r.removeEntriesLocked(vaultID, principalType, principalID)
	return nil
}

func (r *VaultMemberRepository) removeEntriesLocked(vaultID uuid.UUID, principalType domain.PrincipalType, principalID uuid.UUID) {
	rules := r.rules[vaultID]
	for path, rule := range rules {
		kept := rule.Entries[:0]
		for _, e := range rule.Entries {
			if e.PrincipalType != principalType || e.PrincipalID != principalID {
				kept = append(kept, e)
			}
		}
		rule.Entries = kept
		if !rule.Restricted && len(rule.Entries) == 0 {
			delete(rules, path)
		}
	}
}

func (r *VaultMemberRepository) RulesCovering(_ context.Context, vaultID uuid.UUID, path string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []string
	for p := range r.rules[vaultID] {
		if p == path || (domain.IsFolderRule(p) && strings.HasPrefix(path, p)) {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return len(result[i]) < len(result[j]) })
	return result, nil
}

func (r *VaultMemberRepository) RuleExists(_ context.Context, vaultID uuid.UUID, path string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.rules[vaultID][path]
	return ok, nil
}

func (r *VaultMemberRepository) LiveFileExistsUnder(_ context.Context, vaultID uuid.UUID, prefix, exclude string) (bool, error) {
	r.mu.RLock()
	files := r.files
	r.mu.RUnlock()
	if files == nil {
		return false, nil
	}
	files.mu.RLock()
	defer files.mu.RUnlock()
	for _, f := range files.files {
		if f.VaultID == vaultID && !f.Deleted && f.FilePath != exclude && strings.HasPrefix(f.FilePath, prefix) {
			return true, nil
		}
	}
	return false, nil
}

func (r *VaultMemberRepository) CopyRule(_ context.Context, vaultID uuid.UUID, from, to string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rules := r.vaultRules(vaultID)
	source, ok := rules[from]
	if !ok {
		return nil
	}
	if _, exists := rules[to]; exists {
		return nil
	}
	copied := copyRule(source)
	copied.Path = to
	copied.UpdatedAt = time.Now()
	rules[to] = &copied
	return nil
}

func (r *VaultMemberRepository) MoveRule(_ context.Context, vaultID uuid.UUID, from, to string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rules := r.vaultRules(vaultID)
	source, ok := rules[from]
	if !ok {
		return nil
	}
	delete(rules, from)
	source.Path = to
	source.UpdatedAt = time.Now()
	rules[to] = source
	return nil
}

func (r *VaultMemberRepository) DeleteRule(_ context.Context, vaultID uuid.UUID, path string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rules := r.rules[vaultID]
	if _, ok := rules[path]; !ok {
		return false, nil
	}
	delete(rules, path)
	return true, nil
}
