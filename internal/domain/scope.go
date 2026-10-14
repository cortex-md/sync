package domain

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

type AccessLevel uint8

const (
	AccessNone AccessLevel = iota
	AccessView
	AccessEdit
)

func (l AccessLevel) String() string {
	switch l {
	case AccessView:
		return "view"
	case AccessEdit:
		return "edit"
	default:
		return "none"
	}
}

func ParseAccessLevel(value string) (AccessLevel, bool) {
	switch value {
	case "view":
		return AccessView, true
	case "edit":
		return AccessEdit, true
	default:
		return AccessNone, false
	}
}

func BaseAccess(role VaultRole) AccessLevel {
	switch role {
	case VaultRoleOwner, VaultRoleAdmin, VaultRoleEditor:
		return AccessEdit
	case VaultRoleViewer:
		return AccessView
	default:
		return AccessNone
	}
}

func HasFullAccess(role VaultRole) bool {
	return role == VaultRoleOwner || role == VaultRoleAdmin
}

type RuleAccess struct {
	Path       string
	Restricted bool
	Entry      AccessLevel
}

type ScopeRule struct {
	Path  string
	Level AccessLevel
}

type Scope struct {
	All   bool
	Base  AccessLevel
	Rules []ScopeRule
}

func FullScope() Scope {
	return Scope{All: true, Base: AccessEdit}
}

func CompileScope(role VaultRole, candidates []RuleAccess) Scope {
	if HasFullAccess(role) {
		return FullScope()
	}
	scope := Scope{Base: BaseAccess(role)}
	if len(candidates) == 0 {
		return scope
	}
	ordered := make([]RuleAccess, len(candidates))
	copy(ordered, candidates)
	sort.SliceStable(ordered, func(i, j int) bool {
		if len(ordered[i].Path) != len(ordered[j].Path) {
			return len(ordered[i].Path) < len(ordered[j].Path)
		}
		return ordered[i].Path < ordered[j].Path
	})

	compiled := make([]ScopeRule, 0, len(ordered))
	for _, c := range ordered {
		if n := len(compiled); n > 0 && compiled[n-1].Path == c.Path {
			continue
		}
		parent := scope.Base
		depth := -1
		for _, r := range compiled {
			if len(r.Path) > depth && pathCovers(r.Path, c.Path) {
				parent = r.Level
				depth = len(r.Path)
			}
		}
		level := c.Entry
		if !c.Restricted && parent > level {
			level = parent
		}
		compiled = append(compiled, ScopeRule{Path: c.Path, Level: level})
	}

	sort.SliceStable(compiled, func(i, j int) bool {
		if len(compiled[i].Path) != len(compiled[j].Path) {
			return len(compiled[i].Path) > len(compiled[j].Path)
		}
		return compiled[i].Path < compiled[j].Path
	})
	scope.Rules = compiled
	return scope
}

func (s Scope) Level(path string) AccessLevel {
	if s.All {
		return AccessEdit
	}
	for _, r := range s.Rules {
		if pathCovers(r.Path, path) {
			return r.Level
		}
	}
	return s.Base
}

func (s Scope) CanRead(path string) bool {
	return s.Level(path) >= AccessView
}

func (s Scope) CanWrite(path string) bool {
	return s.Level(path) >= AccessEdit
}

func (s Scope) SortedRules() []ScopeRule {
	rules := make([]ScopeRule, len(s.Rules))
	copy(rules, s.Rules)
	sort.Slice(rules, func(i, j int) bool { return rules[i].Path < rules[j].Path })
	return rules
}

func (s Scope) Fingerprint() string {
	if s.All {
		return "all"
	}
	parts := make([]string, 0, len(s.Rules)+1)
	parts = append(parts, "base:"+s.Base.String())
	for _, r := range s.SortedRules() {
		parts = append(parts, r.Level.String()+":"+r.Path)
	}
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(strings.Join(parts, "\x00")))
	return strconv.FormatUint(sum.Sum64(), 36)
}

func (s Scope) ReadFilter() (all bool, exact []string, prefixes []string) {
	if s.All || s.Base >= AccessView {
		return true, nil, nil
	}
	for _, r := range s.Rules {
		if r.Level < AccessView {
			continue
		}
		if IsFolderRule(r.Path) {
			prefixes = append(prefixes, r.Path)
		} else {
			exact = append(exact, r.Path)
		}
	}
	return false, exact, prefixes
}

func (s Scope) Unrestricted() bool {
	if s.All {
		return true
	}
	if s.Base < AccessView {
		return false
	}
	for _, r := range s.Rules {
		if r.Level < AccessView {
			return false
		}
	}
	return true
}

func pathCovers(rule, path string) bool {
	if IsFolderRule(rule) {
		return strings.HasPrefix(path, rule)
	}
	return path == rule
}

func IsFolderRule(path string) bool {
	return strings.HasSuffix(path, "/")
}

func NormalizeGrantPath(path string) (string, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(path), "/")
	if trimmed == "" || strings.Contains(trimmed, "..") || strings.Contains(trimmed, "//") {
		return "", false
	}
	return trimmed, true
}

type RuleMove struct {
	From string
	To   string
}

func FolderCarryCandidates(oldPath, newPath string, folderRules []string) []RuleMove {
	var moves []RuleMove
	for _, rule := range folderRules {
		if !IsFolderRule(rule) || !strings.HasPrefix(oldPath, rule) {
			continue
		}
		rest := oldPath[len(rule):]
		if rest == "" || len(newPath) <= len(rest) || !strings.HasSuffix(newPath, rest) {
			continue
		}
		target := newPath[:len(newPath)-len(rest)]
		if !IsFolderRule(target) || strings.HasPrefix(target, rule) || strings.HasPrefix(rule, target) {
			continue
		}
		moves = append(moves, RuleMove{From: rule, To: target})
	}
	return moves
}
