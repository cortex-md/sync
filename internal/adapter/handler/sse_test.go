package handler_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/adapter/handler"
	"github.com/cortexnotes/cortex-sync/internal/adapter/sse"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sseTestHarness struct {
	router    *chi.Mux
	tokenGen  port.TokenGenerator
	broker    *sse.Broker
	eventRepo *fake.SyncEventRepository
}

func newSSETestHarness() *sseTestHarness {
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

	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	inviteUC := usecase.NewVaultInviteUsecase(inviteRepo, memberRepo, keyRepo, userRepo, vaultRepo, fake.NewTransactor())
	fileUC := usecase.NewFileUsecase(snapshotRepo, deltaRepo, latestRepo, eventRepo, memberRepo, userRepo, deviceRepo, blobStorage, fake.NewTransactor())

	broker := sse.NewBroker(64)
	fileUC.SetBroker(broker)

	authHandler := handler.NewAuthHandler(authUC)
	vaultHandler := handler.NewVaultHandler(vaultUC)
	inviteHandler := handler.NewVaultInviteHandler(inviteUC)
	fileHandler := handler.NewFileHandler(fileUC)
	sseHandler := handler.NewSSEHandlerWithPing(broker, eventRepo, memberRepo, 100*time.Millisecond)

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
			r.Post("/files", fileHandler.UploadSnapshot)
			r.Get("/files", fileHandler.DownloadSnapshot)
			r.Delete("/files", fileHandler.DeleteFile)
			r.Post("/files/deltas", fileHandler.UploadDelta)
			r.Get("/files/deltas", fileHandler.DownloadDeltas)
			r.Post("/files/rename", fileHandler.RenameFile)
			r.Get("/files/info", fileHandler.GetFileInfo)
			r.Get("/files/list", fileHandler.ListFiles)
			r.Get("/files/history", fileHandler.GetHistory)
			r.Get("/changes", fileHandler.ListChanges)
			r.Get("/events", sseHandler.Events)
		})
	})

	return &sseTestHarness{
		router:    r,
		tokenGen:  tokenGen,
		broker:    broker,
		eventRepo: eventRepo,
	}
}

func sseRegisterAndLogin(t *testing.T, h *sseTestHarness, email, password string) (string, string) {
	t.Helper()
	deviceID := uuid.New().String()

	body, _ := json.Marshal(map[string]string{
		"email":        email,
		"password":     password,
		"display_name": "Test User",
	})
	req := httptest.NewRequest("POST", "/auth/v1/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	body, _ = json.Marshal(map[string]string{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"device_name": "Test Device",
		"device_type": "desktop",
	})
	req = httptest.NewRequest("POST", "/auth/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["access_token"], deviceID
}

func sseCreateVault(t *testing.T, h *sseTestHarness, accessToken, deviceID, name string) string {
	t.Helper()
	encKey := base64.StdEncoding.EncodeToString([]byte("test-encrypted-key"))
	body, _ := json.Marshal(map[string]string{
		"name":                name,
		"description":         "A test vault",
		"encrypted_vault_key": encKey,
	})
	req := httptest.NewRequest("POST", "/vaults/v1/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["id"].(string)
}

func sseUploadSnapshot(t *testing.T, h *sseTestHarness, accessToken, deviceID, vaultID, filePath string, data []byte) {
	t.Helper()
	req := httptest.NewRequest("POST", "/sync/v1/vaults/"+vaultID+"/files", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	req.Header.Set("X-File-Path", filePath)
	req.Header.Set("X-Local-Hash", "sha256:abc123")
	req.Header.Set("X-Content-Type", "text/markdown")
	req.Header.Set("Content-Length", strconv.Itoa(len(data)))
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

type sseEventParsed struct {
	ID        string
	EventType string
	Data      string
}

type sseReader struct {
	events chan *sseEventParsed
	errCh  chan error
}

func newSSEReader(scanner *bufio.Scanner) *sseReader {
	r := &sseReader{
		events: make(chan *sseEventParsed, 64),
		errCh:  make(chan error, 1),
	}
	go func() {
		var current sseEventParsed
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if current.EventType != "" || current.Data != "" {
					cp := current
					r.events <- &cp
					current = sseEventParsed{}
				}
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				current.ID = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "event: ") {
				current.EventType = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				current.Data = strings.TrimPrefix(line, "data: ")
			}
		}
		if err := scanner.Err(); err != nil {
			r.errCh <- err
		} else {
			r.errCh <- fmt.Errorf("scanner closed")
		}
	}()
	return r
}

func (r *sseReader) next(timeout time.Duration) (*sseEventParsed, error) {
	select {
	case event := <-r.events:
		return event, nil
	case err := <-r.errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for SSE event")
	}
}

func TestSSEHandler_Events_Unauthorized(t *testing.T) {
	h := newSSETestHarness()
	req := httptest.NewRequest("GET", "/sync/v1/vaults/"+uuid.New().String()+"/events", nil)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSSEHandler_Events_InvalidVaultID(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	req := httptest.NewRequest("GET", "/sync/v1/vaults/not-a-uuid/events", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSSEHandler_Events_NonMember(t *testing.T) {
	h := newSSETestHarness()
	ownerToken, ownerDevice := sseRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := sseRegisterAndLogin(t, h, "other@example.com", "password123")
	vaultID := sseCreateVault(t, h, ownerToken, ownerDevice, "Private Vault")

	req := httptest.NewRequest("GET", "/sync/v1/vaults/"+vaultID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	req.Header.Set("X-Device-ID", otherDevice)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestSSEHandler_Events_SSEHeaders(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
}

func TestSSEHandler_Events_PingKeepAlive(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(1 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "ping", event.EventType)
	assert.Equal(t, "{}", event.Data)
}

func TestSSEHandler_Events_ReceiveBrokerEvent(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) > 0
	}, 2*time.Second, 10*time.Millisecond)

	h.broker.Publish(vaultUUID, port.SSEEvent{
		ID:        "42",
		EventType: "file_created",
		Data:      `{"vault_uuid":"` + vaultID + `","file_path":"notes/test.md"}`,
	})

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "42", event.ID)
	assert.Equal(t, "file_created", event.EventType)
	assert.Contains(t, event.Data, "notes/test.md")
}

func TestSSEHandler_Events_FileUploadTriggersSSE(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) > 0
	}, 2*time.Second, 10*time.Millisecond)

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("# Hello"))

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "file_created", event.EventType)
	assert.Contains(t, event.Data, "notes/hello.md")

	var data map[string]any
	err = json.Unmarshal([]byte(event.Data), &data)
	require.NoError(t, err)
	assert.Equal(t, vaultID, data["vault_uuid"])
	assert.Equal(t, "notes/hello.md", data["file_path"])
	assert.Equal(t, float64(1), data["version"])
}

func TestSSEHandler_Events_LastEventIDReplay(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file1.md", []byte("content1"))
	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file2.md", []byte("content2"))
	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file3.md", []byte("content3"))

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	req.Header.Set("Last-Event-ID", "1")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)

	event1, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Contains(t, event1.Data, "file2.md")

	event2, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Contains(t, event2.Data, "file3.md")
}

func TestSSEHandler_Events_LastEventIDReplayInvalidID(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	req.Header.Set("Last-Event-ID", "not-a-number")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(1 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "ping", event.EventType)
}

func TestSSEHandler_Events_FileDeleteTriggersSSE(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/to-delete.md", []byte("content"))

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) > 0
	}, 2*time.Second, 10*time.Millisecond)

	deleteReq := httptest.NewRequest("DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=notes/to-delete.md", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+accessToken)
	deleteReq.Header.Set("X-Device-ID", deviceID)
	deleteRec := httptest.NewRecorder()
	h.router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code, deleteRec.Body.String())

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "file_deleted", event.EventType)
	assert.Contains(t, event.Data, "notes/to-delete.md")
}

func TestSSEHandler_Events_FileRenameTriggersSSE(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "old-name.md", []byte("content"))

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) > 0
	}, 2*time.Second, 10*time.Millisecond)

	renameBody, _ := json.Marshal(map[string]string{
		"old_path": "old-name.md",
		"new_path": "new-name.md",
	})
	renameReq := httptest.NewRequest("POST", "/sync/v1/vaults/"+vaultID+"/files/rename", bytes.NewReader(renameBody))
	renameReq.Header.Set("Authorization", "Bearer "+accessToken)
	renameReq.Header.Set("X-Device-ID", deviceID)
	renameReq.Header.Set("Content-Type", "application/json")
	renameRec := httptest.NewRecorder()
	h.router.ServeHTTP(renameRec, renameReq)
	require.Equal(t, http.StatusOK, renameRec.Code, renameRec.Body.String())

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "file_renamed", event.EventType)
	assert.Contains(t, event.Data, "new-name.md")
	assert.Contains(t, event.Data, "old-name.md")
}

func TestSSEHandler_Events_ContextCancellation(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) > 0
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	resp.Body.Close()

	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) == 0
	}, 2*time.Second, 50*time.Millisecond)
}

func TestSSEHandler_Events_VaultIsolation(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID1 := sseCreateVault(t, h, accessToken, deviceID, "Vault 1")
	vaultID2 := sseCreateVault(t, h, accessToken, deviceID, "Vault 2")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID1+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	vaultUUID1, err := uuid.Parse(vaultID1)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID1) > 0
	}, 2*time.Second, 10*time.Millisecond)

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID2, "other-vault.md", []byte("content"))

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)
	event, err := reader.next(500 * time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, "ping", event.EventType)
}

func TestSSEHandler_Events_MultipleEvents(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	vaultUUID, err := uuid.Parse(vaultID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return h.broker.SubscriberCount(vaultUUID) > 0
	}, 2*time.Second, 10*time.Millisecond)

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file1.md", []byte("content1"))
	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file2.md", []byte("content2"))

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)

	event1, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "file_created", event1.EventType)
	assert.Contains(t, event1.Data, "file1.md")

	event2, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, "file_created", event2.EventType)
	assert.Contains(t, event2.Data, "file2.md")
}

func TestSSEHandler_Events_NonexistentVault(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")

	req := httptest.NewRequest("GET", "/sync/v1/vaults/"+uuid.New().String()+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestSSEHandler_Events_LastEventIDReplayAll(t *testing.T) {
	h := newSSETestHarness()
	accessToken, deviceID := sseRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := sseCreateVault(t, h, accessToken, deviceID, "Test Vault")

	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file1.md", []byte("content1"))
	sseUploadSnapshot(t, h, accessToken, deviceID, vaultID, "file2.md", []byte("content2"))

	srv := httptest.NewServer(h.router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sync/v1/vaults/"+vaultID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Device-ID", deviceID)
	req.Header.Set("Last-Event-ID", "0")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	reader := newSSEReader(scanner)

	event1, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Contains(t, event1.Data, "file1.md")
	assert.Equal(t, "1", event1.ID)

	event2, err := reader.next(2 * time.Second)
	require.NoError(t, err)
	assert.Contains(t, event2.Data, "file2.md")
	assert.Equal(t, "2", event2.ID)
}
