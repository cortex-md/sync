package server_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/auth"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/template"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type templateTestHarness struct {
	*testHarness
	vaultID uuid.UUID
	owner   map[string]string
	viewer  map[string]string
	groupID uuid.UUID
}

func newTemplateTestHarness(t *testing.T) *templateTestHarness {
	t.Helper()
	ctx := context.Background()
	users := fake.NewUserRepository()
	vaults := fake.NewVaultRepository()
	members := fake.NewVaultMemberRepository()
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	vaultID := uuid.New()
	require.NoError(t, vaults.Create(ctx, &domain.Vault{ID: vaultID, Name: "Team", AllowPublicLinks: true}))
	headers := func(role domain.VaultRole, name string) (uuid.UUID, map[string]string) {
		userID, deviceID := uuid.New(), uuid.New()
		require.NoError(t, users.Create(ctx, &domain.User{ID: userID, Email: name + "@example.com", DisplayName: name}))
		require.NoError(t, members.Add(ctx, &domain.VaultMember{VaultID: vaultID, UserID: userID, Role: role, JoinedAt: time.Now()}))
		token, err := tokenGen.GenerateAccessToken(userID, name+"@example.com", deviceID)
		require.NoError(t, err)
		return userID, map[string]string{"Authorization": "Bearer " + token, "X-Device-ID": deviceID.String()}
	}
	_, ownerHeaders := headers(domain.VaultRoleOwner, "Owner")
	viewerID, viewerHeaders := headers(domain.VaultRoleViewer, "Viewer")
	groupID := uuid.New()
	require.NoError(t, members.CreateGroup(ctx, &domain.VaultGroup{ID: groupID, VaultID: vaultID, Name: "Design", MemberIDs: []uuid.UUID{viewerID}}))

	service := template.NewService(template.Deps{
		Templates: fake.NewTemplateRepository(),
		Groups:    members,
		Users:     users,
		Vaults:    vaults,
		Tx:        fake.NewTransactor(),
		Authz:     authz.New(members),
	})
	handler := template.NewHandler(service)

	r := chi.NewRouter()
	r.Get("/template/v1/market", handler.Market)
	r.Get("/template/v1/{slug}", handler.Public)
	r.Group(func(r chi.Router) {
		r.Use(httpx.AuthMiddleware(tokenGen))
		r.Use(httpx.DeviceMiddleware)
		r.Route("/sync/v1/vaults/{vaultID}/templates", func(r chi.Router) {
			r.Get("/", handler.List)
			r.Post("/", handler.Create)
			r.Get("/{templateID}/payload", handler.GetPayload)
			r.Put("/{templateID}/payload", handler.ReplacePayload)
			r.Patch("/{templateID}", handler.Update)
			r.Delete("/{templateID}", handler.Delete)
		})
	})

	return &templateTestHarness{
		testHarness: &testHarness{router: r, tokenGen: tokenGen},
		vaultID:     vaultID,
		owner:       ownerHeaders,
		viewer:      viewerHeaders,
		groupID:     groupID,
	}
}

func (h *templateTestHarness) path(suffix string) string {
	return "/sync/v1/vaults/" + h.vaultID.String() + "/templates" + suffix
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	return out
}

func marketPayload() string {
	return base64.StdEncoding.EncodeToString([]byte(`{"format":1,"kind":"note","files":[{"path":"Daily/Today.md","content":"# {{date}}"}]}`))
}

func TestTemplateHTTPTeamLifecycle(t *testing.T) {
	h := newTemplateTestHarness(t)
	groupID := h.groupID.String()

	rec := doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "team", "group_id": groupID, "name": "Standup",
		"include_samples": true, "payload": base64.StdEncoding.EncodeToString([]byte("sealed")), "encrypted": true,
	}, h.owner)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeJSON[map[string]any](t, rec.Body.Bytes())
	assert.Equal(t, groupID, created["group_id"])
	assert.Equal(t, "Design", created["group_name"])
	assert.Equal(t, "Owner", created["owner_name"])
	assert.Equal(t, "published", created["status"])
	assert.Equal(t, true, created["include_samples"])
	assert.Nil(t, created["url"])
	assert.Contains(t, created, "review_note")
	assert.Contains(t, created, "created_at")
	id := created["id"].(string)

	rec = doRequestWithHeaders(h.testHarness, "GET", h.path("/"), nil, h.viewer)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	list := decodeJSON[[]map[string]any](t, rec.Body.Bytes())
	require.Len(t, list, 1)
	assert.Equal(t, id, list[0]["id"])

	rec = doRequestWithHeaders(h.testHarness, "GET", h.path("/"+id+"/payload"), nil, h.viewer)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	payload := decodeJSON[map[string]any](t, rec.Body.Bytes())
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("sealed")), payload["payload"])
	assert.Equal(t, true, payload["encrypted"])
	assert.EqualValues(t, 1, payload["version"])

	rec = doRequestWithHeaders(h.testHarness, "PATCH", h.path("/"+id), map[string]any{"name": "Nope"}, h.viewer)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(h.testHarness, "PATCH", h.path("/"+id), map[string]any{"group_id": nil, "description": "Daily"}, h.owner)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	patched := decodeJSON[map[string]any](t, rec.Body.Bytes())
	assert.Nil(t, patched["group_id"])
	assert.Nil(t, patched["group_name"])
	assert.Equal(t, "Daily", patched["description"])

	rec = doRequestWithHeaders(h.testHarness, "PUT", h.path("/"+id+"/payload"), map[string]any{
		"payload": base64.StdEncoding.EncodeToString([]byte("sealed-2")), "encrypted": true,
	}, h.owner)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.EqualValues(t, 2, decodeJSON[map[string]any](t, rec.Body.Bytes())["version"])

	slug := patched["slug"].(string)
	rec = doRequest(h.testHarness, "GET", "/template/v1/"+slug, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = doRequestWithHeaders(h.testHarness, "DELETE", h.path("/"+id), nil, h.owner)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = doRequestWithHeaders(h.testHarness, "GET", h.path("/"+id+"/payload"), nil, h.owner)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTemplateHTTPMarketAndPublicLink(t *testing.T) {
	h := newTemplateTestHarness(t)

	rec := doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "market", "name": "Daily journal", "description": "Simple daily page",
		"payload": marketPayload(), "encrypted": false,
	}, h.owner)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeJSON[map[string]any](t, rec.Body.Bytes())
	assert.Equal(t, "published", created["status"])
	slug := created["slug"].(string)
	assert.Equal(t, "/t/"+slug, created["url"])

	rec = doRequest(h.testHarness, "GET", "/template/v1/market?kind=note&q=journal", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	market := decodeJSON[[]map[string]any](t, rec.Body.Bytes())
	require.Len(t, market, 1)
	assert.Equal(t, slug, market[0]["slug"])
	assert.Equal(t, "Owner", market[0]["owner_name"])
	assert.EqualValues(t, 1, market[0]["file_count"])
	assert.NotContains(t, market[0], "payload")

	rec = doRequest(h.testHarness, "GET", "/template/v1/market?kind=bogus", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doRequest(h.testHarness, "GET", "/template/v1/"+slug, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	public := decodeJSON[map[string]any](t, rec.Body.Bytes())
	assert.Equal(t, marketPayload(), public["payload"])
	assert.Equal(t, "Daily journal", public["template"].(map[string]any)["name"])

	rec = doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "market", "name": "Evil",
		"payload": base64.StdEncoding.EncodeToString([]byte(`{"format":1,"kind":"note","files":[{"path":"../x.md","content":""}]}`)),
	}, h.owner)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rejected := decodeJSON[map[string]any](t, rec.Body.Bytes())
	assert.Equal(t, "rejected", rejected["status"])
	assert.NotEmpty(t, rejected["review_note"])
	rec = doRequest(h.testHarness, "GET", "/template/v1/"+rejected["slug"].(string), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTemplateHTTPValidation(t *testing.T) {
	h := newTemplateTestHarness(t)

	rec := doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "link", "name": "x", "payload": "%%%",
	}, h.owner)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "link", "name": "x", "payload": marketPayload(), "encrypted": true,
	}, h.owner)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "link", "name": "x",
		"payload": base64.StdEncoding.EncodeToString(make([]byte, template.MaxPayloadBytes+1)),
	}, h.owner)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	rec = doRequestWithHeaders(h.testHarness, "POST", h.path("/"), map[string]any{
		"kind": "note", "audience": "team", "name": "x", "payload": marketPayload(), "encrypted": true,
	}, h.viewer)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = doRequestWithHeaders(h.testHarness, "GET", h.path("/not-a-uuid/payload"), nil, h.owner)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
