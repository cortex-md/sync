package vault

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "Group", GroupResponse{})
	apidocstest.AssertMatches(t, "AccessEntry", AccessEntryPayload{})
	apidocstest.AssertMatches(t, "AccessRule", AccessRuleResponse{})
	apidocstest.AssertMatches(t, "EffectiveAccess", EffectiveAccessResponse{})
	apidocstest.AssertMatches(t, "CreateGroupRequest", createGroupRequest{})
	apidocstest.AssertMatches(t, "UpdateGroupRequest", updateGroupRequest{})
	apidocstest.AssertMatches(t, "VaultEncryption", vaultEncryptionResponse{})
	apidocstest.AssertMatches(t, "CreateVaultEncryptionRequest", createVaultEncryptionRequest{})
	apidocstest.AssertMatches(t, "InviteGrant", GrantPayload{})
	apidocstest.AssertMatches(t, "CreateInviteRequest", createInviteRequest{})
	apidocstest.AssertMatches(t, "Invite", InviteResponse{})
	apidocstest.AssertMatches(t, "AcceptInviteRequest", acceptInviteRequest{})
	apidocstest.AssertMatches(t, "AcceptedVault", acceptInviteResponse{})
	apidocstest.AssertMatches(t, "InvitePreview", invitePreviewResponse{})
	apidocstest.AssertMatches(t, "VaultKey", vaultKeyResponse{})
	apidocstest.AssertMatches(t, "VaultKeyRequest", vaultKeyRequest{})
	apidocstest.AssertMatches(t, "PendingKey", pendingKeyResponse{})
	apidocstest.AssertMatches(t, "PublicKeyRequest", publicKeyRequest{})
	apidocstest.AssertMatches(t, "UserPublicKey", publicKeyResponse{})
	apidocstest.AssertMatches(t, "Member", MemberResponse{})
	apidocstest.AssertMatches(t, "UpdateMemberRoleRequest", updateMemberRoleRequest{})
	apidocstest.AssertMatches(t, "CreateVaultRequest", createVaultRequest{})
	apidocstest.AssertMatches(t, "Vault", VaultResponse{})
	apidocstest.AssertMatches(t, "UpdateVaultRequest", updateVaultRequest{})
}
