package domain_test

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestCompileScopeMatrix(t *testing.T) {
	rules := []domain.RuleAccess{
		{Path: "hr/", Restricted: true},
		{Path: "hr/public/", Restricted: false, Entry: domain.AccessView},
		{Path: "eng/", Restricted: true, Entry: domain.AccessEdit},
		{Path: "eng/secret/", Restricted: true},
		{Path: "docs/", Restricted: false, Entry: domain.AccessEdit},
		{Path: "docs/locked.md", Restricted: true, Entry: domain.AccessView},
	}

	cases := []struct {
		role  domain.VaultRole
		path  string
		level domain.AccessLevel
	}{
		{domain.VaultRoleOwner, "hr/salaries.md", domain.AccessEdit},
		{domain.VaultRoleAdmin, "eng/secret/plan.md", domain.AccessEdit},

		{domain.VaultRoleEditor, "notes/a.md", domain.AccessEdit},
		{domain.VaultRoleEditor, "hr/salaries.md", domain.AccessNone},
		{domain.VaultRoleEditor, "hr/public/handbook.md", domain.AccessView},
		{domain.VaultRoleEditor, "eng/design.md", domain.AccessEdit},
		{domain.VaultRoleEditor, "eng/secret/plan.md", domain.AccessNone},
		{domain.VaultRoleEditor, "docs/guide.md", domain.AccessEdit},
		{domain.VaultRoleEditor, "docs/locked.md", domain.AccessView},

		{domain.VaultRoleViewer, "notes/a.md", domain.AccessView},
		{domain.VaultRoleViewer, "docs/guide.md", domain.AccessEdit},
		{domain.VaultRoleViewer, "hr/public/handbook.md", domain.AccessView},
		{domain.VaultRoleViewer, "eng/design.md", domain.AccessEdit},

		{domain.VaultRoleGuest, "notes/a.md", domain.AccessNone},
		{domain.VaultRoleGuest, "docs/guide.md", domain.AccessEdit},
		{domain.VaultRoleGuest, "docs/locked.md", domain.AccessView},
		{domain.VaultRoleGuest, "hr/public/handbook.md", domain.AccessView},
		{domain.VaultRoleGuest, "eng/secret/plan.md", domain.AccessNone},
	}
	for _, c := range cases {
		scope := domain.CompileScope(c.role, rules)
		assert.Equal(t, c.level, scope.Level(c.path), "%s at %s", c.role, c.path)
		assert.Equal(t, c.level >= domain.AccessView, scope.CanRead(c.path), "%s read %s", c.role, c.path)
		assert.Equal(t, c.level >= domain.AccessEdit, scope.CanWrite(c.path), "%s write %s", c.role, c.path)
	}
}

func TestCompileScopeInheritsFromNearestCompiledAncestor(t *testing.T) {
	scope := domain.CompileScope(domain.VaultRoleEditor, []domain.RuleAccess{
		{Path: "a/", Restricted: true, Entry: domain.AccessView},
		{Path: "a/b/c/", Restricted: false, Entry: domain.AccessNone},
		{Path: "a/b/c/d/", Restricted: false, Entry: domain.AccessEdit},
	})
	assert.Equal(t, domain.AccessView, scope.Level("a/x.md"))
	assert.Equal(t, domain.AccessView, scope.Level("a/b/c/x.md"))
	assert.Equal(t, domain.AccessEdit, scope.Level("a/b/c/d/x.md"))
	assert.Equal(t, domain.AccessEdit, scope.Level("z.md"))
}

func TestCompileScopeRestrictedWithoutEntryHidesFromViewersAndEditors(t *testing.T) {
	for _, role := range []domain.VaultRole{domain.VaultRoleViewer, domain.VaultRoleEditor, domain.VaultRoleGuest} {
		scope := domain.CompileScope(role, []domain.RuleAccess{{Path: "private/", Restricted: true}})
		assert.False(t, scope.CanRead("private/a.md"), role)
		assert.False(t, scope.Unrestricted(), role)
	}
}

func TestScopeReadFilter(t *testing.T) {
	guest := domain.CompileScope(domain.VaultRoleGuest, []domain.RuleAccess{
		{Path: "shared/", Entry: domain.AccessView},
		{Path: "shared/hidden/", Restricted: true},
		{Path: "note.md", Entry: domain.AccessEdit},
	})
	all, exact, prefixes := guest.ReadFilter()
	assert.False(t, all)
	assert.Equal(t, []string{"note.md"}, exact)
	assert.Equal(t, []string{"shared/"}, prefixes)
	assert.False(t, guest.CanRead("shared/hidden/x.md"))

	editor := domain.CompileScope(domain.VaultRoleEditor, nil)
	all, _, _ = editor.ReadFilter()
	assert.True(t, all)
	assert.True(t, editor.Unrestricted())
}

func TestScopeFingerprintTracksEffectiveAccess(t *testing.T) {
	a := domain.CompileScope(domain.VaultRoleViewer, []domain.RuleAccess{{Path: "x/", Entry: domain.AccessEdit}})
	b := domain.CompileScope(domain.VaultRoleViewer, []domain.RuleAccess{{Path: "x/", Entry: domain.AccessView}})
	c := domain.CompileScope(domain.VaultRoleViewer, []domain.RuleAccess{{Path: "x/", Entry: domain.AccessEdit}})
	assert.NotEqual(t, a.Fingerprint(), b.Fingerprint())
	assert.Equal(t, a.Fingerprint(), c.Fingerprint())
	assert.Equal(t, "all", domain.CompileScope(domain.VaultRoleOwner, nil).Fingerprint())
}

func TestFolderCarryCandidates(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		rules    []string
		want     []domain.RuleMove
	}{
		{"folder rename carries rule", "A/B/x.md", "A/C/x.md", []string{"A/B/"}, []domain.RuleMove{{From: "A/B/", To: "A/C/"}}},
		{"nested folder rename carries each level", "A/B/x.md", "Z/B/x.md", []string{"A/", "A/B/"},
			[]domain.RuleMove{{From: "A/", To: "Z/"}, {From: "A/B/", To: "Z/B/"}}},
		{"move into another folder keeps name only", "A/x.md", "B/y.md", []string{"A/"}, nil},
		{"move to root", "A/x.md", "x.md", []string{"A/"}, nil},
		{"move within the rule folder", "A/x.md", "A/B/x.md", []string{"A/"}, nil},
		{"move to ancestor of rule", "A/B/x.md", "A/x.md", []string{"A/B/"}, nil},
		{"file rules are ignored", "A/x.md", "B/x.md", []string{"A/x.md"}, nil},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, domain.FolderCarryCandidates(c.old, c.new, c.rules), c.name)
	}
}
