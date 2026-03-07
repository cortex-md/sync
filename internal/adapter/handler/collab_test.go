package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/collab"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/adapter/handler"
	"github.com/cortexnotes/cortex-sync/internal/adapter/sse"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type collabTestHarness struct {
	router     *chi.Mux
	tokenGen   port.TokenGenerator
	sseBroker  *sse.Broker
	collabRepo *fake.CollabDocumentRepository
	memberRepo *fake.VaultMemberRepository
}

func newCollabTestHarness() *collabTestHarness {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	keyRepo := fake.NewVaultKeyRepository()
	snapshotRepo := fake.NewFileSnapshotRepository()
	deltaRepo := fake.NewFileDeltaRepository()
	latestRepo := fake.NewFileLatestRepository()
	eventRepo := fake.NewSyncEventRepository()
	blobStorage := fake.NewBlobStorage()
	collabRepo := fake.NewCollabDocumentRepository()

	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	inviteUC := usecase.NewVaultInviteUsecase(inviteRepo, memberRepo, keyRepo, userRepo, vaultRepo, fake.NewTransactor())
	fileUC := usecase.NewFileUsecase(snapshotRepo, deltaRepo, latestRepo, eventRepo, memberRepo, blobStorage, fake.NewTransactor())

	sseBroker := sse.NewBroker(64)
	fileUC.SetBroker(sseBroker)

	collabBroker := collab.NewBroker(10, 0)

	authHandler := handler.NewAuthHandler(authUC)
	vaultHandler := handler.NewVaultHandler(vaultUC)
	inviteHandler := handler.NewVaultInviteHandler(inviteUC)
	collabHandler := handler.NewCollabHandler(collabBroker, collabRepo, memberRepo, tokenGen, sseBroker, 10*time.Second)

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
	})
	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Route("/vaults/v1", func(r chi.Router) {
			r.Post("/", vaultHandler.Create)
			r.Get("/invites", inviteHandler.ListMyInvites)
			r.Post("/invites/accept", inviteHandler.Accept)
			r.Route("/{vaultID}", func(r chi.Router) {
				r.Route("/invites", func(r chi.Router) {
					r.Post("/", inviteHandler.Create)
				})
			})
		})
		r.Route("/sync/v1/vaults/{vaultID}", func(r chi.Router) {
			r.Get("/collab/peers", collabHandler.GetPeers)
		})
	})

	r.Get("/sync/v1/vaults/{vaultID}/collab", collabHandler.Connect)

	return &collabTestHarness{
		router:     r,
		tokenGen:   tokenGen,
		sseBroker:  sseBroker,
		collabRepo: collabRepo,
		memberRepo: memberRepo,
	}
}

func collabRegisterAndLogin(t *testing.T, h *collabTestHarness, email, password string) (accessToken string, deviceID string) {
	t.Helper()
	deviceID = uuid.New().String()

	body, _ := json.Marshal(map[string]string{
		"email": email, "password": password, "display_name": "Test User",
	})
	req := httptest.NewRequest("POST", "/auth/v1/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	body, _ = json.Marshal(map[string]string{
		"email": email, "password": password,
		"device_id": deviceID, "device_name": "Dev", "device_type": "desktop",
	})
	req = httptest.NewRequest("POST", "/auth/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["access_token"], deviceID
}

func collabCreateVault(t *testing.T, h *collabTestHarness, accessToken, deviceID string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"name": "Test Vault", "description": "desc", "encrypted_vault_key": "dGVzdA==",
	})
	req := httptest.NewRequest("POST", "/vaults/v1/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["id"].(string)
}

func collabDial(t *testing.T, srv *httptest.Server, vaultID, filePath, token string) *websocket.Conn {
	t.Helper()
	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?path=%s&token=%s",
		strings.Replace(srv.URL, "http://", "ws://", 1),
		vaultID, filePath, token,
	)
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	require.NoError(t, err)
	return conn
}

func TestCollabHandler_Connect_MissingPath(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, _ := h.tokenGen.GenerateAccessToken(uuid.New(), "x@x.com")
	vaultID := uuid.New().String()

	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?token=%s",
		strings.Replace(srv.URL, "http://", "ws://", 1), vaultID, token)
	_, resp, err := websocket.Dial(context.Background(), url, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
}

func TestCollabHandler_Connect_MissingToken(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	vaultID := uuid.New().String()
	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?path=doc.md",
		strings.Replace(srv.URL, "http://", "ws://", 1), vaultID)
	_, resp, err := websocket.Dial(context.Background(), url, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestCollabHandler_Connect_InvalidToken(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	vaultID := uuid.New().String()
	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?path=doc.md&token=bad",
		strings.Replace(srv.URL, "http://", "ws://", 1), vaultID)
	_, resp, err := websocket.Dial(context.Background(), url, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestCollabHandler_Connect_NotAMember(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, _ := h.tokenGen.GenerateAccessToken(uuid.New(), "outsider@x.com")
	vaultID := uuid.New().String()

	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?path=doc.md&token=%s",
		strings.Replace(srv.URL, "http://", "ws://", 1), vaultID, token)
	_, resp, err := websocket.Dial(context.Background(), url, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
}

func TestCollabHandler_Connect_Success_AndGetPeers(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "user@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	conn := collabDial(t, srv, vaultID, "notes/doc.md", token)
	defer conn.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	req := httptest.NewRequest("GET", fmt.Sprintf("/sync/v1/vaults/%s/collab/peers?path=notes/doc.md", vaultID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(1), body["peer_count"])
	peerIDs, ok := body["peer_ids"].([]any)
	require.True(t, ok, "peer_ids should be an array")
	assert.Len(t, peerIDs, 1)
}

func TestCollabHandler_Connect_PublishesCollabActiveSSE(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "active@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)

	sseCh, err := h.sseBroker.Subscribe(context.Background(), vaultUUID, "test-subscriber")
	require.NoError(t, err)
	defer h.sseBroker.Unsubscribe(vaultUUID, "test-subscriber")

	conn := collabDial(t, srv, vaultID, "doc.md", token)
	defer conn.Close(websocket.StatusNormalClosure, "")

	select {
	case event := <-sseCh:
		assert.Equal(t, string(domain.EventCollabActive), event.EventType)
		assert.Contains(t, event.Data, "doc.md")
	case <-time.After(2 * time.Second):
		t.Fatal("expected collab_active SSE event but timed out")
	}
}

func TestCollabHandler_Connect_PublishesCollabInactiveSSE(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "inactive@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)

	conn := collabDial(t, srv, vaultID, "notes.md", token)

	time.Sleep(50 * time.Millisecond)

	sseCh, err := h.sseBroker.Subscribe(context.Background(), vaultUUID, "test-subscriber-inactive")
	require.NoError(t, err)
	defer h.sseBroker.Unsubscribe(vaultUUID, "test-subscriber-inactive")

	conn.Close(websocket.StatusNormalClosure, "")

	select {
	case event := <-sseCh:
		assert.Equal(t, string(domain.EventCollabInactive), event.EventType)
		assert.Contains(t, event.Data, "notes.md")
	case <-time.After(2 * time.Second):
		t.Fatal("expected collab_inactive SSE event but timed out")
	}
}

func TestCollabHandler_Connect_BroadcastsUpdatesToOtherPeers(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token1, deviceID1 := collabRegisterAndLogin(t, h, "peer1@x.com", "password123")
	vaultID := collabCreateVault(t, h, token1, deviceID1)

	token2, _ := collabRegisterAndLogin(t, h, "peer2@x.com", "password123")
	vaultUUID, _ := uuid.Parse(vaultID)
	err := h.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vaultUUID,
		UserID:  uuid.MustParse(extractUserIDFromToken(t, h.tokenGen, token2)),
		Role:    domain.VaultRoleEditor,
	})
	require.NoError(t, err)

	conn1 := collabDial(t, srv, vaultID, "shared.md", token1)
	defer conn1.Close(websocket.StatusNormalClosure, "")

	conn2 := collabDial(t, srv, vaultID, "shared.md", token2)
	defer conn2.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	update := map[string]any{"type": "update", "data": []byte{0x01, 0x02}}
	require.NoError(t, wsjson.Write(context.Background(), conn1, update))

	var received map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = wsjson.Read(ctx, conn2, &received)
	require.NoError(t, err)
	assert.Equal(t, "update", received["type"])
}

func TestCollabHandler_Connect_ViewerCannotSendUpdates(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token1, deviceID1 := collabRegisterAndLogin(t, h, "owner@x.com", "password123")
	vaultID := collabCreateVault(t, h, token1, deviceID1)

	token2, _ := collabRegisterAndLogin(t, h, "viewer@x.com", "password123")
	vaultUUID, _ := uuid.Parse(vaultID)
	err := h.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vaultUUID,
		UserID:  uuid.MustParse(extractUserIDFromToken(t, h.tokenGen, token2)),
		Role:    domain.VaultRoleViewer,
	})
	require.NoError(t, err)

	connOwner := collabDial(t, srv, vaultID, "doc.md", token1)
	defer connOwner.Close(websocket.StatusNormalClosure, "")

	connViewer := collabDial(t, srv, vaultID, "doc.md", token2)
	defer connViewer.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	update := map[string]any{"type": "update", "data": []byte{0xFF}}
	require.NoError(t, wsjson.Write(context.Background(), connViewer, update))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var received map[string]any
	err = wsjson.Read(ctx, connOwner, &received)
	assert.Error(t, err, "owner should not receive update from viewer")
}

func TestCollabHandler_Connect_PingPong(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "ping@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	conn := collabDial(t, srv, vaultID, "doc.md", token)
	defer conn.Close(websocket.StatusNormalClosure, "")

	ping := map[string]any{"type": "pong"}
	require.NoError(t, wsjson.Write(context.Background(), conn, ping))
}

func TestCollabHandler_GetPeers_Unauthorized(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	vaultID := uuid.New().String()
	req := httptest.NewRequest("GET", fmt.Sprintf("/sync/v1/vaults/%s/collab/peers?path=doc.md", vaultID), nil)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCollabHandler_GetPeers_NotAMember(t *testing.T) {
	h := newCollabTestHarness()

	token, deviceID := collabRegisterAndLogin(t, h, "nonmember@x.com", "password123")
	vaultID := uuid.New().String()

	req := httptest.NewRequest("GET", fmt.Sprintf("/sync/v1/vaults/%s/collab/peers?path=doc.md", vaultID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCollabHandler_GetPeers_MissingPath(t *testing.T) {
	h := newCollabTestHarness()

	token, deviceID := collabRegisterAndLogin(t, h, "npath@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	req := httptest.NewRequest("GET", fmt.Sprintf("/sync/v1/vaults/%s/collab/peers", vaultID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCollabHandler_GetPeers_ZeroWhenNoConnections(t *testing.T) {
	h := newCollabTestHarness()

	token, deviceID := collabRegisterAndLogin(t, h, "zero@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	req := httptest.NewRequest("GET", fmt.Sprintf("/sync/v1/vaults/%s/collab/peers?path=doc.md", vaultID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(0), body["peer_count"])
	peerIDs, ok := body["peer_ids"].([]any)
	require.True(t, ok, "peer_ids should be an empty array")
	assert.Empty(t, peerIDs)
}

func TestCollabHandler_Connect_CompactMessage(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "compact@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)
	vaultUUID, _ := uuid.Parse(vaultID)

	conn := collabDial(t, srv, vaultID, "compact.md", token)
	defer conn.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	compact := map[string]any{"type": "compact", "data": []byte("full-state")}
	require.NoError(t, wsjson.Write(context.Background(), conn, compact))

	time.Sleep(100 * time.Millisecond)

	doc, updates, err := h.collabRepo.LoadDocument(context.Background(), vaultUUID, "compact.md")
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Empty(t, updates, "compaction should clear incremental updates")
}

func extractUserIDFromToken(t *testing.T, tokenGen port.TokenGenerator, token string) string {
	t.Helper()
	claims, err := tokenGen.ValidateAccessToken(token)
	require.NoError(t, err)
	return claims.UserID.String()
}
