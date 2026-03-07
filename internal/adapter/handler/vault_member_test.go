package handler_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addMemberViaInvite(t *testing.T, h *vaultTestHarness, ownerToken, ownerDevice, inviteeToken, inviteeDevice, inviteeEmail, vaultID, role string) {
	t.Helper()
	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("member-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       inviteeEmail,
		"role":                role,
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, vaultAuthHeaders(inviteeToken, inviteeDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestVaultMemberHandler_List_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)
	assert.Len(t, members, 2)

	roles := map[string]bool{}
	for _, m := range members {
		roles[m["role"].(string)] = true
	}
	assert.True(t, roles["owner"])
	assert.True(t, roles["editor"])
}

func TestVaultMemberHandler_List_NotMember(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := vaultRegisterAndLogin(t, h, "other@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Private Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(otherToken, otherDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultMemberHandler_UpdateRole_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)

	var editorUserID string
	for _, m := range members {
		if m["role"].(string) == "editor" {
			editorUserID = m["user_id"].(string)
		}
	}
	require.NotEmpty(t, editorUserID)

	rec = doRequestWithHeaders(th, "PATCH", "/vaults/v1/"+vaultID+"/members/"+editorUserID, map[string]string{
		"role": "viewer",
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestVaultMemberHandler_UpdateRole_InvalidRole(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)

	var editorUserID string
	for _, m := range members {
		if m["role"].(string) == "editor" {
			editorUserID = m["user_id"].(string)
		}
	}
	require.NotEmpty(t, editorUserID)

	rec = doRequestWithHeaders(th, "PATCH", "/vaults/v1/"+vaultID+"/members/"+editorUserID, map[string]string{
		"role": "superadmin",
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultMemberHandler_UpdateRole_InsufficientRole(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")
	viewerToken, viewerDevice := vaultRegisterAndLogin(t, h, "viewer@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")
	addMemberViaInvite(t, h, ownerToken, ownerDevice, viewerToken, viewerDevice, "viewer@example.com", vaultID, "viewer")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)

	var viewerUserID string
	for _, m := range members {
		if m["role"].(string) == "viewer" {
			viewerUserID = m["user_id"].(string)
		}
	}
	require.NotEmpty(t, viewerUserID)

	rec = doRequestWithHeaders(th, "PATCH", "/vaults/v1/"+vaultID+"/members/"+viewerUserID, map[string]string{
		"role": "admin",
	}, vaultAuthHeaders(editorToken, editorDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultMemberHandler_Remove_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)

	var editorUserID string
	for _, m := range members {
		if m["role"].(string) == "editor" {
			editorUserID = m["user_id"].(string)
		}
	}
	require.NotEmpty(t, editorUserID)

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/members/"+editorUserID, nil, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	err = json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)
	assert.Len(t, members, 1)
}

func TestVaultMemberHandler_Remove_SelfLeave(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)

	var editorUserID string
	for _, m := range members {
		if m["role"].(string) == "editor" {
			editorUserID = m["user_id"].(string)
		}
	}
	require.NotEmpty(t, editorUserID)

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/members/"+editorUserID, nil, vaultAuthHeaders(editorToken, editorDevice))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestVaultMemberHandler_Remove_OwnerCannotLeave(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "My Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/members/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var members []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&members)
	require.NoError(t, err)
	require.Len(t, members, 1)

	ownerUserID := members[0]["user_id"].(string)

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/members/"+ownerUserID, nil, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultMemberHandler_InvalidVaultUUID(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/not-a-uuid/members/", nil, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultMemberHandler_InvalidUserUUID(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "My Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "PATCH", "/vaults/v1/"+vaultID+"/members/not-a-uuid", map[string]string{
		"role": "editor",
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
