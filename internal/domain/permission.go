package domain

type Permission uint32

const (
	PermFileRead Permission = 1 << iota
	PermFileWrite
	PermMemberRead
	PermMemberManage
	PermInviteManage
	PermKeyGrant
	PermVaultUpdate
	PermVaultDelete
	PermNoteShare
)

var rolePermissions = map[VaultRole]Permission{
	VaultRoleGuest:  PermFileRead | PermFileWrite,
	VaultRoleViewer: PermFileRead | PermMemberRead,
	VaultRoleEditor: PermFileRead | PermMemberRead | PermFileWrite | PermNoteShare,
	VaultRoleAdmin: PermFileRead | PermMemberRead | PermFileWrite | PermNoteShare |
		PermMemberManage | PermInviteManage | PermKeyGrant | PermVaultUpdate,
	VaultRoleOwner: PermFileRead | PermMemberRead | PermFileWrite | PermNoteShare |
		PermMemberManage | PermInviteManage | PermKeyGrant | PermVaultUpdate | PermVaultDelete,
}

var roleRank = map[VaultRole]int{
	VaultRoleGuest:  0,
	VaultRoleViewer: 1,
	VaultRoleEditor: 2,
	VaultRoleAdmin:  3,
	VaultRoleOwner:  4,
}

func (r VaultRole) Valid() bool {
	_, ok := roleRank[r]
	return ok
}

func (r VaultRole) Can(p Permission) bool {
	return rolePermissions[r]&p == p
}

func (r VaultRole) Outranks(other VaultRole) bool {
	return roleRank[r] > roleRank[other]
}

func (r VaultRole) CanAssign(target VaultRole, newRole VaultRole) bool {
	return r.Can(PermMemberManage) && newRole.Valid() && newRole != VaultRoleOwner &&
		r.Outranks(target) && (r == VaultRoleOwner || r.Outranks(newRole))
}
