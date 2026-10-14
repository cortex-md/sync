package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/realtime"
	"github.com/cortexnotes/cortex-sync/internal/server"
	"github.com/cortexnotes/cortex-sync/internal/vault"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/cortexnotes/cortex-sync/internal/auth"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type collabTestHarness struct {
	router     *chi.Mux
	tokenGen   server.TokenGenerator
	sseBroker  *realtime.EventBroker
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
	collabRepo := fake.NewCollabDocumentRepository()
	vaultRepo.ResolveSummariesWith(memberRepo, keyRepo)
	inviteRepo.ResolveDetailsWith(vaultRepo, userRepo)
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	authUC := auth.NewService(auth.Deps{Users: userRepo, Devices: deviceRepo, RefreshTokens: refreshTokenRepo, Hasher: hasher, Tokens: tokenGen, RefreshExpiry: 90 * 24 * time.Hour})
	vaultUC := vault.NewService(vault.Deps{Vaults: vaultRepo, Members: memberRepo, Keys: keyRepo, Invites: inviteRepo, Tx: fake.NewTransactor()})
	inviteUC := vault.NewInviteService(vault.InviteDeps{Invites: inviteRepo, Members: memberRepo, Keys: keyRepo, Users: userRepo, Vaults: vaultRepo, Tx: fake.NewTransactor()})
	sseBroker := realtime.NewEventBroker(64)

	collabBroker := realtime.NewRoomBroker(10, 0)
	signalBus := bus.NewMemory()
	signalBus.Subscribe(bus.KindSSE, func(signal bus.Signal) {
		sseBroker.Publish(signal.VaultID, realtime.Event{EventType: signal.Event, Data: string(signal.Data)})
	})

	authHandler := auth.NewHandler(authUC)
	vaultHandler := vault.NewHandler(vaultUC)
	inviteHandler := vault.NewInviteHandler(inviteUC)
	collabHandler := realtime.NewCollabHandler(realtime.CollabDeps{
		Broker:        collabBroker,
		Documents:     collabRepo,
		Presence:      fake.NewCollabPresenceRepository(),
		Members:       memberRepo,
		Users:         userRepo,
		Bus:           signalBus,
		Tickets:       realtime.NewTicketSigner("test-secret", time.Minute),
		FlushInterval: 10 * time.Second,
	})

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.AuthMiddleware(tokenGen))
		r.Use(httpx.DeviceMiddleware)
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
			r.Post("/collab/tickets", collabHandler.CreateTicket)
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

	var resp stringFields
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

func collabCreateTicket(t *testing.T, h *collabTestHarness, vaultID, filePath, token, deviceID string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"file_path": filePath})
	req := httptest.NewRequest("POST", fmt.Sprintf("/sync/v1/vaults/%s/collab/tickets", vaultID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp stringFields
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp["ticket"])
	return resp["ticket"]
}

func collabDial(t *testing.T, h *collabTestHarness, srv *httptest.Server, vaultID, filePath, token, deviceID string) *websocket.Conn {
	return collabDialQuery(t, h, srv, vaultID, filePath, token, deviceID, "")
}

func collabDialQuery(t *testing.T, h *collabTestHarness, srv *httptest.Server, vaultID, filePath, token, deviceID, extra string) *websocket.Conn {
	t.Helper()
	ticket := collabCreateTicket(t, h, vaultID, filePath, token, deviceID)
	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?path=%s&ticket=%s%s",
		strings.Replace(srv.URL, "http://", "ws://", 1),
		vaultID, url.QueryEscape(filePath), ticket, extra,
	)
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	require.NoError(t, err)
	return conn
}

func TestCollabHandler_Connect_MissingPath(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	vaultID := uuid.New().String()

	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?ticket=bad",
		strings.Replace(srv.URL, "http://", "ws://", 1), vaultID)
	_, resp, err := websocket.Dial(context.Background(), url, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
}

func TestCollabHandler_Connect_MissingTicket(t *testing.T) {
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

func TestCollabHandler_Connect_InvalidTicket(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	vaultID := uuid.New().String()
	url := fmt.Sprintf("%s/sync/v1/vaults/%s/collab?path=doc.md&ticket=bad",
		strings.Replace(srv.URL, "http://", "ws://", 1), vaultID)
	_, resp, err := websocket.Dial(context.Background(), url, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestCollabHandler_CreateTicket_NotAMember(t *testing.T) {
	h := newCollabTestHarness()

	token, deviceID := collabRegisterAndLogin(t, h, "outsider@x.com", "password123")
	vaultID := uuid.New().String()

	body, _ := json.Marshal(map[string]string{"file_path": "doc.md"})
	req := httptest.NewRequest("POST", fmt.Sprintf("/sync/v1/vaults/%s/collab/tickets", vaultID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCollabHandler_Connect_Success_AndGetPeers(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "user@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	conn := collabDial(t, h, srv, vaultID, "notes/doc.md", token, deviceID)
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

	conn := collabDial(t, h, srv, vaultID, "doc.md", token, deviceID)
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

	conn := collabDial(t, h, srv, vaultID, "notes.md", token, deviceID)

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

	token2, deviceID2 := collabRegisterAndLogin(t, h, "peer2@x.com", "password123")
	vaultUUID, _ := uuid.Parse(vaultID)
	err := h.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vaultUUID,
		UserID:  uuid.MustParse(extractUserIDFromToken(t, h.tokenGen, token2)),
		Role:    domain.VaultRoleEditor,
	})
	require.NoError(t, err)

	conn1 := collabDial(t, h, srv, vaultID, "shared.md", token1, deviceID1)
	defer conn1.Close(websocket.StatusNormalClosure, "")

	conn2 := collabDial(t, h, srv, vaultID, "shared.md", token2, deviceID2)
	defer conn2.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	update := map[string]any{"type": "update", "data": []byte{0x01, 0x02}}
	require.NoError(t, wsjson.Write(context.Background(), conn1, update))

	received, err := collabReadUntil(conn2, "update", 2*time.Second)
	require.NoError(t, err)
	from, ok := received["from"].(map[string]any)
	require.True(t, ok, "relayed messages must carry the server-stamped sender")
	assert.Equal(t, extractUserIDFromToken(t, h.tokenGen, token1), from["user_id"])
	assert.Equal(t, "Test User", from["display_name"])
}

func TestCollabHandler_BinaryAndJSONClientsShareRoom(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token1, deviceID1 := collabRegisterAndLogin(t, h, "bin@x.com", "password123")
	vaultID := collabCreateVault(t, h, token1, deviceID1)
	token2, deviceID2 := collabRegisterAndLogin(t, h, "json@x.com", "password123")
	require.NoError(t, h.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: uuid.MustParse(vaultID),
		UserID:  uuid.MustParse(extractUserIDFromToken(t, h.tokenGen, token2)),
		Role:    domain.VaultRoleEditor,
	}))

	binConn := collabDialQuery(t, h, srv, vaultID, "shared.md", token1, deviceID1, "&frames=binary")
	defer binConn.Close(websocket.StatusNormalClosure, "")
	jsonConn := collabDial(t, h, srv, vaultID, "shared.md", token2, deviceID2)
	defer jsonConn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(50 * time.Millisecond)

	ctx := context.Background()
	require.NoError(t, binConn.Write(ctx, websocket.MessageBinary, []byte{3, 0xAA, 0xBB}))
	received, err := collabReadUntil(jsonConn, "update", 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "qrs=", received["data"])

	require.NoError(t, wsjson.Write(ctx, jsonConn, map[string]any{"type": "update", "data": []byte{0x01, 0x02}}))
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		typ, frame, err := binConn.Read(readCtx)
		require.NoError(t, err)
		if typ != websocket.MessageBinary {
			continue
		}
		require.Equal(t, byte(3), frame[0])
		idLen := int(frame[1])
		assert.NotZero(t, idLen)
		assert.Equal(t, []byte{0x01, 0x02}, frame[2+idLen:])
		return
	}
}

func TestCollabHandler_AwarenessIdentityCannotBeSpoofed(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token1, deviceID1 := collabRegisterAndLogin(t, h, "spoof1@x.com", "password123")
	vaultID := collabCreateVault(t, h, token1, deviceID1)
	token2, deviceID2 := collabRegisterAndLogin(t, h, "spoof2@x.com", "password123")
	require.NoError(t, h.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: uuid.MustParse(vaultID),
		UserID:  uuid.MustParse(extractUserIDFromToken(t, h.tokenGen, token2)),
		Role:    domain.VaultRoleViewer,
	}))

	conn1 := collabDial(t, h, srv, vaultID, "doc.md", token1, deviceID1)
	defer conn1.Close(websocket.StatusNormalClosure, "")
	conn2 := collabDial(t, h, srv, vaultID, "doc.md", token2, deviceID2)
	defer conn2.Close(websocket.StatusNormalClosure, "")

	forged := map[string]any{
		"type": "awareness",
		"data": []byte("cursor"),
		"from": map[string]any{"user_id": extractUserIDFromToken(t, h.tokenGen, token1), "display_name": "Owner"},
	}
	require.NoError(t, wsjson.Write(context.Background(), conn2, forged))

	received, err := collabReadUntil(conn1, "awareness", 2*time.Second)
	require.NoError(t, err)
	from := received["from"].(map[string]any)
	assert.Equal(t, extractUserIDFromToken(t, h.tokenGen, token2), from["user_id"])
	assert.Equal(t, false, from["can_write"])
}

func collabReadUntil(conn *websocket.Conn, msgType string, timeout time.Duration) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		var msg map[string]any
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return nil, err
		}
		if msg["type"] == msgType {
			return msg, nil
		}
	}
}

func TestCollabHandler_Connect_ViewerCannotSendUpdates(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token1, deviceID1 := collabRegisterAndLogin(t, h, "owner@x.com", "password123")
	vaultID := collabCreateVault(t, h, token1, deviceID1)

	token2, deviceID2 := collabRegisterAndLogin(t, h, "viewer@x.com", "password123")
	vaultUUID, _ := uuid.Parse(vaultID)
	err := h.memberRepo.Add(context.Background(), &domain.VaultMember{
		VaultID: vaultUUID,
		UserID:  uuid.MustParse(extractUserIDFromToken(t, h.tokenGen, token2)),
		Role:    domain.VaultRoleViewer,
	})
	require.NoError(t, err)

	connOwner := collabDial(t, h, srv, vaultID, "doc.md", token1, deviceID1)
	defer connOwner.Close(websocket.StatusNormalClosure, "")

	connViewer := collabDial(t, h, srv, vaultID, "doc.md", token2, deviceID2)
	defer connViewer.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	update := map[string]any{"type": "update", "data": []byte{0xFF}}
	require.NoError(t, wsjson.Write(context.Background(), connViewer, update))

	_, err = collabReadUntil(connOwner, "update", 300*time.Millisecond)
	assert.Error(t, err, "owner should not receive update from viewer")
}

func TestCollabHandler_Connect_PingPong(t *testing.T) {
	h := newCollabTestHarness()
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	token, deviceID := collabRegisterAndLogin(t, h, "ping@x.com", "password123")
	vaultID := collabCreateVault(t, h, token, deviceID)

	conn := collabDial(t, h, srv, vaultID, "doc.md", token, deviceID)
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

	first := collabDial(t, h, srv, vaultID, "compact.md", token, deviceID)
	require.NoError(t, wsjson.Write(context.Background(), first, map[string]any{"type": "update", "data": []byte{0x01}}))
	time.Sleep(50 * time.Millisecond)
	first.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)

	conn := collabDial(t, h, srv, vaultID, "compact.md", token, deviceID)
	defer conn.Close(websocket.StatusNormalClosure, "")
	welcome, err := collabReadUntil(conn, "welcome", 2*time.Second)
	require.NoError(t, err)
	upTo := welcome["up_to"].(float64)
	require.Positive(t, upTo, "flush on leave must persist the buffered update")

	compact := map[string]any{"type": "compact", "data": []byte("full-state"), "up_to": upTo}
	require.NoError(t, wsjson.Write(context.Background(), conn, compact))

	time.Sleep(100 * time.Millisecond)

	doc, updates, err := h.collabRepo.LoadDocument(context.Background(), vaultUUID, "compact.md")
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Equal(t, []byte("full-state"), doc.CompactedState)
	assert.Empty(t, updates, "compaction should clear incremental updates up to the given id")
}

func extractUserIDFromToken(t *testing.T, tokenGen server.TokenGenerator, token string) string {
	t.Helper()
	claims, err := tokenGen.ValidateAccessToken(token)
	require.NoError(t, err)
	return claims.UserID.String()
}
