package realtime_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/realtime"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type collabHarness struct {
	srv     *httptest.Server
	broker  *realtime.RoomBroker
	docs    *fake.CollabDocumentRepository
	users   *fake.UserRepository
	members *fake.VaultMemberRepository
	tickets *realtime.TicketSigner
	vaultID uuid.UUID
}

type wireMessage struct {
	Type     string `json:"type"`
	Data     []byte `json:"data"`
	UpTo     int64  `json:"up_to"`
	ClientID string `json:"client_id"`
}

func newCollabHarness(t *testing.T) *collabHarness {
	t.Helper()
	h := &collabHarness{
		broker:  realtime.NewRoomBroker(10, 0),
		docs:    fake.NewCollabDocumentRepository(),
		users:   fake.NewUserRepository(),
		members: fake.NewVaultMemberRepository(),
		tickets: realtime.NewTicketSigner("test-secret", time.Minute),
		vaultID: uuid.New(),
	}
	handler := realtime.NewCollabHandler(realtime.CollabDeps{
		Broker:        h.broker,
		Documents:     h.docs,
		Presence:      fake.NewCollabPresenceRepository(),
		Members:       h.members,
		Users:         h.users,
		Bus:           bus.NewMemory(),
		Tickets:       h.tickets,
		FlushInterval: time.Hour,
	})
	r := chi.NewRouter()
	r.Get("/vaults/{vaultID}/collab", handler.Connect)
	h.srv = httptest.NewServer(r)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *collabHarness) addMember(t *testing.T, role domain.VaultRole) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, h.users.Create(ctx, &domain.User{ID: userID, Email: userID.String() + "@x.com", DisplayName: string(role)}))
	require.NoError(t, h.members.Add(ctx, &domain.VaultMember{VaultID: h.vaultID, UserID: userID, Role: role, JoinedAt: time.Now()}))
	return userID
}

func (h *collabHarness) dial(t *testing.T, userID uuid.UUID, filePath string, binary bool) *websocket.Conn {
	t.Helper()
	ticket, _, err := h.tickets.Issue(userID, h.vaultID, filePath)
	require.NoError(t, err)
	target := strings.Replace(h.srv.URL, "http://", "ws://", 1) + "/vaults/" + h.vaultID.String() +
		"/collab?path=" + url.QueryEscape(filePath) + "&ticket=" + url.QueryEscape(ticket)
	if binary {
		target += "&frames=binary"
	}
	conn, _, err := websocket.Dial(context.Background(), target, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	require.NoError(t, err)
	return typ, data
}

func readJSON(t *testing.T, conn *websocket.Conn) wireMessage {
	t.Helper()
	typ, data := readFrame(t, conn)
	require.Equal(t, websocket.MessageText, typ)
	var msg wireMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

func readUntilSynced(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		if readJSON(t, conn).Type == "synced" {
			return
		}
	}
}

func writeJSON(t *testing.T, conn *websocket.Conn, msg map[string]any) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.Write(context.Background(), websocket.MessageText, data))
}

func TestCollab_InitialStateOrderEndsWithSynced(t *testing.T) {
	h := newCollabHarness(t)
	ctx := context.Background()
	const path = "notes/a.md"
	require.NoError(t, h.docs.BatchStoreUpdates(ctx, h.vaultID, path, [][]byte{{0x01}, {0x02}, {0x03}}))
	require.NoError(t, h.docs.CompactDocument(ctx, h.vaultID, path, []byte("compacted"), 1))

	writer := h.addMember(t, domain.VaultRoleEditor)
	first := h.dial(t, writer, path, false)
	readUntilSynced(t, first)
	writeJSON(t, first, map[string]any{"type": "update", "data": []byte("pending")})
	require.Eventually(t, func() bool { return len(h.broker.PendingUpdates(h.vaultID, path)) == 1 }, 2*time.Second, 10*time.Millisecond)

	second := h.dial(t, h.addMember(t, domain.VaultRoleViewer), path, false)
	welcome := readJSON(t, second)
	assert.Equal(t, "welcome", welcome.Type)
	assert.Equal(t, int64(3), welcome.UpTo)
	assert.NotEmpty(t, welcome.ClientID)

	expected := []wireMessage{
		{Type: "sync_step2", Data: []byte("compacted")},
		{Type: "update", Data: []byte{0x02}},
		{Type: "update", Data: []byte{0x03}},
		{Type: "update", Data: []byte("pending")},
		{Type: "synced"},
	}
	for _, want := range expected {
		got := readJSON(t, second)
		assert.Equal(t, want.Type, got.Type)
		assert.Equal(t, want.Data, got.Data)
	}

	binary := h.dial(t, writer, path, true)
	assert.Equal(t, "welcome", readJSON(t, binary).Type)
	for _, want := range []struct {
		opcode  byte
		payload []byte
	}{{2, []byte("compacted")}, {3, []byte{0x02}}, {3, []byte{0x03}}, {3, []byte("pending")}} {
		typ, data := readFrame(t, binary)
		require.Equal(t, websocket.MessageBinary, typ)
		require.GreaterOrEqual(t, len(data), 2)
		assert.Equal(t, want.opcode, data[0])
		assert.Equal(t, want.payload, data[2+int(data[1]):])
	}
	assert.Equal(t, "synced", readJSON(t, binary).Type)
}

func TestCollab_EmptyDocumentSendsWelcomeThenSynced(t *testing.T) {
	h := newCollabHarness(t)
	conn := h.dial(t, h.addMember(t, domain.VaultRoleEditor), "empty.md", false)
	assert.Equal(t, "welcome", readJSON(t, conn).Type)
	assert.Equal(t, "synced", readJSON(t, conn).Type)
}

func TestCollab_ViewerUpdateIsDropped(t *testing.T) {
	h := newCollabHarness(t)
	const path = "notes/view.md"
	editor := h.dial(t, h.addMember(t, domain.VaultRoleEditor), path, false)
	readUntilSynced(t, editor)
	viewer := h.dial(t, h.addMember(t, domain.VaultRoleViewer), path, false)
	readUntilSynced(t, viewer)

	writeJSON(t, viewer, map[string]any{"type": "update", "data": []byte("viewer-edit")})
	writeJSON(t, viewer, map[string]any{"type": "sync_step2", "data": []byte("viewer-state")})
	writeJSON(t, viewer, map[string]any{"type": "sync_step1", "data": []byte("viewer-sv")})

	var seen []string
	for {
		msg := readJSON(t, editor)
		if msg.Type == "sync_step1" {
			assert.Equal(t, []byte("viewer-sv"), msg.Data)
			break
		}
		seen = append(seen, msg.Type)
	}
	assert.NotContains(t, seen, "update")
	assert.NotContains(t, seen, "sync_step2")
	assert.Empty(t, h.broker.PendingUpdates(h.vaultID, path))

	_, updates, err := h.docs.LoadDocument(context.Background(), h.vaultID, path)
	require.NoError(t, err)
	assert.Empty(t, updates)
}

func TestCollab_CompactDeletesUpdatesUpToAndStoresState(t *testing.T) {
	h := newCollabHarness(t)
	ctx := context.Background()
	const path = "notes/compact.md"
	require.NoError(t, h.docs.BatchStoreUpdates(ctx, h.vaultID, path, [][]byte{{0x01}, {0x02}}))

	conn := h.dial(t, h.addMember(t, domain.VaultRoleEditor), path, false)
	welcome := readJSON(t, conn)
	require.Equal(t, int64(2), welcome.UpTo)
	readUntilSynced(t, conn)

	require.NoError(t, h.docs.BatchStoreUpdates(ctx, h.vaultID, path, [][]byte{{0x03}}))
	writeJSON(t, conn, map[string]any{"type": "compact", "data": []byte("full-state"), "up_to": welcome.UpTo})

	require.Eventually(t, func() bool {
		doc, _, err := h.docs.LoadDocument(ctx, h.vaultID, path)
		return err == nil && doc != nil && string(doc.CompactedState) == "full-state"
	}, 2*time.Second, 10*time.Millisecond)

	_, updates, err := h.docs.LoadDocument(ctx, h.vaultID, path)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, []byte{0x03}, updates[0].Data)
}
