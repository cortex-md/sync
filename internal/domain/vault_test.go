package domain_test

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestVaultRole_CanWrite(t *testing.T) {
	tests := []struct {
		role     domain.VaultRole
		expected bool
	}{
		{domain.VaultRoleOwner, true},
		{domain.VaultRoleAdmin, true},
		{domain.VaultRoleEditor, true},
		{domain.VaultRoleViewer, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.role.CanWrite())
		})
	}
}

func TestVaultRole_CanManageMembers(t *testing.T) {
	tests := []struct {
		role     domain.VaultRole
		expected bool
	}{
		{domain.VaultRoleOwner, true},
		{domain.VaultRoleAdmin, true},
		{domain.VaultRoleEditor, false},
		{domain.VaultRoleViewer, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.role.CanManageMembers())
		})
	}
}

func TestVaultRole_CanDelete(t *testing.T) {
	tests := []struct {
		role     domain.VaultRole
		expected bool
	}{
		{domain.VaultRoleOwner, true},
		{domain.VaultRoleAdmin, false},
		{domain.VaultRoleEditor, false},
		{domain.VaultRoleViewer, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.role.CanDelete())
		})
	}
}
