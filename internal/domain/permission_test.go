package domain_test

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestRoleCanAssign(t *testing.T) {
	cases := []struct {
		actor, target, next domain.VaultRole
		allowed             bool
	}{
		{domain.VaultRoleOwner, domain.VaultRoleEditor, domain.VaultRoleAdmin, true},
		{domain.VaultRoleOwner, domain.VaultRoleAdmin, domain.VaultRoleViewer, true},
		{domain.VaultRoleOwner, domain.VaultRoleEditor, domain.VaultRoleOwner, false},
		{domain.VaultRoleAdmin, domain.VaultRoleViewer, domain.VaultRoleEditor, true},
		{domain.VaultRoleAdmin, domain.VaultRoleEditor, domain.VaultRoleAdmin, false},
		{domain.VaultRoleAdmin, domain.VaultRoleAdmin, domain.VaultRoleViewer, false},
		{domain.VaultRoleAdmin, domain.VaultRoleOwner, domain.VaultRoleViewer, false},
		{domain.VaultRoleEditor, domain.VaultRoleViewer, domain.VaultRoleEditor, false},
		{domain.VaultRoleOwner, domain.VaultRoleViewer, domain.VaultRole("root"), false},
	}
	for _, c := range cases {
		assert.Equal(t, c.allowed, c.actor.CanAssign(c.target, c.next), "%s assigning %s -> %s", c.actor, c.target, c.next)
	}
}

func TestRolePermissions(t *testing.T) {
	assert.True(t, domain.VaultRoleViewer.Can(domain.PermFileRead))
	assert.False(t, domain.VaultRoleViewer.Can(domain.PermFileWrite))
	assert.True(t, domain.VaultRoleEditor.Can(domain.PermFileWrite))
	assert.False(t, domain.VaultRoleEditor.Can(domain.PermKeyGrant))
	assert.True(t, domain.VaultRoleAdmin.Can(domain.PermKeyGrant|domain.PermInviteManage))
	assert.False(t, domain.VaultRoleAdmin.Can(domain.PermVaultDelete))
	assert.True(t, domain.VaultRoleOwner.Can(domain.PermVaultDelete))
	assert.False(t, domain.VaultRole("").Can(domain.PermFileRead))
}
