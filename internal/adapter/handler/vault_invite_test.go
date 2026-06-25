package handler_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVaultInviteHandler_Create_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("invitee-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	assert.NotEmpty(t, invite["id"])
	assert.Equal(t, vaultID, invite["vault_id"])
	assert.Equal(t, "invitee@example.com", invite["invitee_email"])
	assert.Equal(t, "editor", invite["role"])
	assert.Equal(t, "Team Vault", invite["vault_name"])
}

func TestVaultInviteHandler_Create_InvalidRole(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "superadmin",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultInviteHandler_Create_OwnerRole(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "owner",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultInviteHandler_Create_InvalidBase64Key(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": "not-valid-base64!@#$",
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultInviteHandler_Create_NotMember(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := vaultRegisterAndLogin(t, h, "other@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Private Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "someone@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(otherToken, otherDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultInviteHandler_Create_AlreadyMember(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "editor@example.com",
		"role":                "viewer",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestVaultInviteHandler_ListByVault_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/invites/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var invites []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invites)
	require.NoError(t, err)
	assert.Len(t, invites, 1)
	assert.Equal(t, "invitee@example.com", invites[0]["invitee_email"])
}

func TestVaultInviteHandler_ListByVault_InsufficientRole(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	addMemberViaInvite(t, h, ownerToken, ownerDevice, editorToken, editorDevice, "editor@example.com", vaultID, "editor")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/invites/", nil, vaultAuthHeaders(editorToken, editorDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultInviteHandler_ListMyInvites_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	inviteeToken, inviteeDevice := vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = doRequestWithHeaders(th, "GET", "/vaults/v1/invites", nil, vaultAuthHeaders(inviteeToken, inviteeDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var invites []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invites)
	require.NoError(t, err)
	assert.Len(t, invites, 1)
	assert.Equal(t, "Team Vault", invites[0]["vault_name"])
	assert.NotEmpty(t, invites[0]["encrypted_vault_key"])
}

func TestVaultInviteHandler_ListMyInvites_Empty(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "user@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/invites", nil, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var invites []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invites)
	require.NoError(t, err)
	assert.Len(t, invites, 0)
}

func TestVaultInviteHandler_Accept_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	inviteeToken, inviteeDevice := vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("invitee-vault-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, vaultAuthHeaders(inviteeToken, inviteeDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var vault map[string]any
	err = json.NewDecoder(rec.Body).Decode(&vault)
	require.NoError(t, err)
	assert.Equal(t, vaultID, vault["id"])
	assert.Equal(t, "editor", vault["role"])
	assert.Equal(t, "Team Vault", vault["name"])
}

func TestVaultInviteHandler_Accept_WrongUser(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")
	wrongToken, wrongDevice := vaultRegisterAndLogin(t, h, "wrong@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, vaultAuthHeaders(wrongToken, wrongDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultInviteHandler_Accept_InvalidInviteID(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "user@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": "not-a-uuid",
	}, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultInviteHandler_Accept_NonexistentInvite(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "user@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": uuid.New().String(),
	}, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVaultInviteHandler_Accept_AlreadyAccepted(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	inviteeToken, inviteeDevice := vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, vaultAuthHeaders(inviteeToken, inviteeDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, vaultAuthHeaders(inviteeToken, inviteeDevice))
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestVaultInviteHandler_Delete_Success(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/invites/"+inviteID, nil, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/invites/", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusOK, rec.Code)

	var invites []map[string]any
	err = json.NewDecoder(rec.Body).Decode(&invites)
	require.NoError(t, err)
	assert.Len(t, invites, 0)
}

func TestVaultInviteHandler_Delete_NotMember(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := vaultRegisterAndLogin(t, h, "other@example.com", "password123")
	vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/invites/"+inviteID, nil, vaultAuthHeaders(otherToken, otherDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultInviteHandler_Delete_InvalidInviteUUID(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Team Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/invites/not-a-uuid", nil, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultInviteHandler_Delete_WrongVault(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	vaultRegisterAndLogin(t, h, "invitee@example.com", "password123")

	created1 := createVault(t, h, ownerToken, ownerDevice, "Vault 1")
	vaultID1 := created1["id"].(string)

	created2 := createVault(t, h, ownerToken, ownerDevice, "Vault 2")
	vaultID2 := created2["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID1+"/invites/", map[string]string{
		"invitee_email":       "invitee@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code)

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID2+"/invites/"+inviteID, nil, vaultAuthHeaders(ownerToken, ownerDevice))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
