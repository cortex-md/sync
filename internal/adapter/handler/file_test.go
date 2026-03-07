package handler_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/adapter/handler"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fileTestHarness struct {
	router   *chi.Mux
	tokenGen port.TokenGenerator
	fileUC   *usecase.FileUsecase
}

func newFileTestHarness() *fileTestHarness {
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
	fileUC := usecase.NewFileUsecase(snapshotRepo, deltaRepo, latestRepo, eventRepo, memberRepo, blobStorage, fake.NewTransactor())

	authHandler := handler.NewAuthHandler(authUC)
	vaultHandler := handler.NewVaultHandler(vaultUC)
	inviteHandler := handler.NewVaultInviteHandler(inviteUC)
	fileHandler := handler.NewFileHandler(fileUC)

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
			r.Post("/files/bulk", fileHandler.BulkGetFileInfo)
			r.Get("/files/info", fileHandler.GetFileInfo)
			r.Get("/files/list", fileHandler.ListFiles)
			r.Get("/files/history", fileHandler.GetHistory)
			r.Get("/changes", fileHandler.ListChanges)
		})
	})

	return &fileTestHarness{router: r, tokenGen: tokenGen, fileUC: fileUC}
}

func fileRegisterAndLogin(t *testing.T, h *fileTestHarness, email, password string) (string, string) {
	t.Helper()
	deviceID := uuid.New().String()
	th := &testHarness{router: h.router, tokenGen: h.tokenGen}

	rec := doRequestWithHeaders(th, "POST", "/auth/v1/register", map[string]string{
		"email":        email,
		"password":     password,
		"display_name": "Test User",
	}, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "POST", "/auth/v1/login", map[string]string{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"device_name": "Test Device",
		"device_type": "desktop",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["access_token"], deviceID
}

func fileAuthHeaders(accessToken, deviceID string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + accessToken,
		"X-Device-ID":   deviceID,
	}
}

func fileCreateVault(t *testing.T, h *fileTestHarness, accessToken, deviceID, name string) string {
	t.Helper()
	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("test-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/", map[string]string{
		"name":                name,
		"description":         "A test vault",
		"encrypted_vault_key": encKey,
	}, fileAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["id"].(string)
}

func doRawRequest(h *fileTestHarness, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func fileDoJSON(h *fileTestHarness, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func fileContentChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:8])
}

func uploadSnapshot(t *testing.T, h *fileTestHarness, accessToken, deviceID, vaultID, filePath string, data []byte) map[string]any {
	t.Helper()
	headers := fileAuthHeaders(accessToken, deviceID)
	headers["X-File-Path"] = filePath
	headers["X-Local-Hash"] = fileContentChecksum(data)
	headers["X-Content-Type"] = "text/markdown"
	headers["Content-Length"] = strconv.Itoa(len(data))

	rec := doRawRequest(h, "POST", "/sync/v1/vaults/"+vaultID+"/files", bytes.NewReader(data), headers)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp
}

func TestFileHandler_UploadSnapshot_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	data := []byte("# Hello World\nThis is encrypted content.")
	resp := uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", data)

	assert.Equal(t, vaultID, resp["vault_id"])
	assert.Equal(t, "notes/hello.md", resp["file_path"])
	assert.Equal(t, float64(1), resp["version"])
	assert.NotEmpty(t, resp["checksum"])
	assert.Equal(t, false, resp["deleted"])
	assert.Equal(t, "text/markdown", resp["content_type"])
	assert.NotEmpty(t, resp["snapshot_id"])
}

func TestFileHandler_UploadSnapshot_SecondVersion(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v1 content"))
	resp := uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v2 content"))

	assert.Equal(t, float64(2), resp["version"])
}

func TestFileHandler_UploadSnapshot_MissingFilePath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "POST", "/sync/v1/vaults/"+vaultID+"/files", bytes.NewReader([]byte("data")), headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_UploadSnapshot_InvalidVaultID(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")

	headers := fileAuthHeaders(accessToken, deviceID)
	headers["X-File-Path"] = "test.md"
	rec := doRawRequest(h, "POST", "/sync/v1/vaults/not-a-uuid/files", bytes.NewReader([]byte("data")), headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_UploadSnapshot_NotMember(t *testing.T) {
	h := newFileTestHarness()
	ownerToken, ownerDevice := fileRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := fileRegisterAndLogin(t, h, "other@example.com", "password123")
	vaultID := fileCreateVault(t, h, ownerToken, ownerDevice, "Private Vault")

	headers := fileAuthHeaders(otherToken, otherDevice)
	headers["X-File-Path"] = "test.md"
	headers["X-Local-Hash"] = "sha256:abc"
	rec := doRawRequest(h, "POST", "/sync/v1/vaults/"+vaultID+"/files", bytes.NewReader([]byte("data")), headers)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestFileHandler_UploadSnapshot_Unauthorized(t *testing.T) {
	h := newFileTestHarness()
	rec := doRawRequest(h, "POST", "/sync/v1/vaults/"+uuid.New().String()+"/files", bytes.NewReader([]byte("data")), nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestFileHandler_DownloadSnapshot_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	originalData := []byte("# Hello World\nEncrypted content here.")
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", originalData)

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files?path=notes/hello.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "notes/hello.md", rec.Header().Get("X-File-Path"))
	assert.Equal(t, "1", rec.Header().Get("X-File-Version"))
	assert.NotEmpty(t, rec.Header().Get("X-Checksum"))
	assert.Equal(t, originalData, rec.Body.Bytes())
}

func TestFileHandler_DownloadSnapshot_SpecificVersion(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	v1Data := []byte("version 1")
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", v1Data)
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("version 2"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files?path=notes/hello.md&version=1", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, "1", rec.Header().Get("X-File-Version"))
	assert.Equal(t, v1Data, rec.Body.Bytes())
}

func TestFileHandler_DownloadSnapshot_NotFound(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files?path=nonexistent.md", nil, headers)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFileHandler_DownloadSnapshot_MissingPath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files", nil, headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_DownloadSnapshot_NotMember(t *testing.T) {
	h := newFileTestHarness()
	ownerToken, ownerDevice := fileRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := fileRegisterAndLogin(t, h, "other@example.com", "password123")
	vaultID := fileCreateVault(t, h, ownerToken, ownerDevice, "Private Vault")

	uploadSnapshot(t, h, ownerToken, ownerDevice, vaultID, "secret.md", []byte("secret"))

	headers := fileAuthHeaders(otherToken, otherDevice)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files?path=secret.md", nil, headers)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestFileHandler_DeleteFile_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("content"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=notes/hello.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=notes/hello.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	json.NewDecoder(rec.Body).Decode(&info)
	assert.Equal(t, true, info["deleted"])
}

func TestFileHandler_DeleteFile_NotFound(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=nonexistent.md", nil, headers)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFileHandler_DeleteFile_MissingPath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files", nil, headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_DeleteFile_AlreadyDeleted(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("content"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=notes/hello.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=notes/hello.md", nil, headers)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFileHandler_RenameFile_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/old.md", []byte("content"))

	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/rename", map[string]string{
		"old_path": "notes/old.md",
		"new_path": "notes/new.md",
	}, fileAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	assert.Equal(t, "notes/new.md", resp["file_path"])
	assert.Equal(t, false, resp["deleted"])
}

func TestFileHandler_RenameFile_SamePath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/test.md", []byte("content"))

	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/rename", map[string]string{
		"old_path": "notes/test.md",
		"new_path": "notes/test.md",
	}, fileAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_RenameFile_TargetExists(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))

	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/rename", map[string]string{
		"old_path": "notes/a.md",
		"new_path": "notes/b.md",
	}, fileAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestFileHandler_RenameFile_SourceNotFound(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/rename", map[string]string{
		"old_path": "nonexistent.md",
		"new_path": "new.md",
	}, fileAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFileHandler_UploadDelta_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v1 content"))

	encDelta := base64.StdEncoding.EncodeToString([]byte("encrypted-delta-data"))
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      "notes/hello.md",
		"base_version":   1,
		"checksum":       "sha256:def456",
		"size_bytes":     100,
		"encrypted_data": encDelta,
	}, fileAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	assert.Equal(t, float64(2), resp["version"])
	assert.Equal(t, "notes/hello.md", resp["file_path"])
}

func TestFileHandler_UploadDelta_VersionConflict(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v1 content"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v2 content"))

	encDelta := base64.StdEncoding.EncodeToString([]byte("encrypted-delta"))
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      "notes/hello.md",
		"base_version":   1,
		"checksum":       "sha256:def456",
		"size_bytes":     100,
		"encrypted_data": encDelta,
	}, fileAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestFileHandler_UploadDelta_InvalidBase64(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      "notes/hello.md",
		"base_version":   1,
		"checksum":       "sha256:def456",
		"size_bytes":     100,
		"encrypted_data": "not-valid-base64!@#$",
	}, fileAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_UploadDelta_FileNotFound(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	encDelta := base64.StdEncoding.EncodeToString([]byte("delta"))
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      "nonexistent.md",
		"base_version":   1,
		"checksum":       "sha256:def",
		"size_bytes":     10,
		"encrypted_data": encDelta,
	}, fileAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFileHandler_DownloadDeltas_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	encDelta := base64.StdEncoding.EncodeToString([]byte("delta-1"))
	fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      "notes/hello.md",
		"base_version":   1,
		"checksum":       "sha256:d1",
		"size_bytes":     50,
		"encrypted_data": encDelta,
	}, fileAuthHeaders(accessToken, deviceID))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/deltas?path=notes/hello.md&since_version=0", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var deltas []map[string]any
	json.NewDecoder(rec.Body).Decode(&deltas)
	assert.Len(t, deltas, 1)
	assert.Equal(t, float64(1), deltas[0]["base_version"])
	assert.Equal(t, float64(2), deltas[0]["target_version"])
	assert.NotEmpty(t, deltas[0]["encrypted_delta"])
}

func TestFileHandler_DownloadDeltas_MissingPath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/deltas", nil, headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_GetFileInfo_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("content"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=notes/hello.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var info map[string]any
	json.NewDecoder(rec.Body).Decode(&info)
	assert.Equal(t, "notes/hello.md", info["file_path"])
	assert.Equal(t, float64(1), info["version"])
	assert.Equal(t, false, info["deleted"])
}

func TestFileHandler_GetFileInfo_NotFound(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=nonexistent.md", nil, headers)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFileHandler_GetFileInfo_MissingPath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info", nil, headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_ListFiles_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/c.md", []byte("content c"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/list", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var files []map[string]any
	json.NewDecoder(rec.Body).Decode(&files)
	assert.Len(t, files, 3)
}

func TestFileHandler_ListFiles_Empty(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/list", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var files []map[string]any
	json.NewDecoder(rec.Body).Decode(&files)
	assert.Len(t, files, 0)
}

func TestFileHandler_ListFiles_ExcludesDeleted(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))

	headers := fileAuthHeaders(accessToken, deviceID)
	doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=notes/b.md", nil, headers)

	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/list", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	var files []map[string]any
	json.NewDecoder(rec.Body).Decode(&files)
	assert.Len(t, files, 1)
	assert.Equal(t, "notes/a.md", files[0]["file_path"])
}

func TestFileHandler_ListFiles_IncludeDeleted(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))

	headers := fileAuthHeaders(accessToken, deviceID)
	doRawRequest(h, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path=notes/b.md", nil, headers)

	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/list?include_deleted=true", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	var files []map[string]any
	json.NewDecoder(rec.Body).Decode(&files)
	assert.Len(t, files, 2)
}

func TestFileHandler_ListFiles_NotMember(t *testing.T) {
	h := newFileTestHarness()
	ownerToken, ownerDevice := fileRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := fileRegisterAndLogin(t, h, "other@example.com", "password123")
	vaultID := fileCreateVault(t, h, ownerToken, ownerDevice, "Private Vault")

	headers := fileAuthHeaders(otherToken, otherDevice)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/list", nil, headers)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestFileHandler_GetHistory_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v1"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v2"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v3"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/history?path=notes/hello.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var entries []map[string]any
	json.NewDecoder(rec.Body).Decode(&entries)
	assert.Len(t, entries, 3)
}

func TestFileHandler_GetHistory_Empty(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/history?path=nonexistent.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	var entries []map[string]any
	json.NewDecoder(rec.Body).Decode(&entries)
	assert.Len(t, entries, 0)
}

func TestFileHandler_GetHistory_MissingPath(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/history", nil, headers)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFileHandler_ListChanges_Success(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/changes?since=0", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var events []map[string]any
	json.NewDecoder(rec.Body).Decode(&events)
	assert.GreaterOrEqual(t, len(events), 2)

	assert.Equal(t, "file_created", events[0]["event_type"])
}

func TestFileHandler_ListChanges_WithLimit(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/c.md", []byte("content c"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/changes?since=0&limit=1", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	var events []map[string]any
	json.NewDecoder(rec.Body).Decode(&events)
	assert.Len(t, events, 1)
}

func TestFileHandler_ListChanges_SinceEvent(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))

	headers := fileAuthHeaders(accessToken, deviceID)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/changes?since=0&limit=100", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	var allEvents []map[string]any
	json.NewDecoder(rec.Body).Decode(&allEvents)
	require.GreaterOrEqual(t, len(allEvents), 2)

	firstEventID := int64(allEvents[0]["id"].(float64))

	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/changes?since="+strconv.FormatInt(firstEventID, 10), nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	var laterEvents []map[string]any
	json.NewDecoder(rec.Body).Decode(&laterEvents)
	assert.Less(t, len(laterEvents), len(allEvents))
}

func TestFileHandler_ListChanges_NotMember(t *testing.T) {
	h := newFileTestHarness()
	ownerToken, ownerDevice := fileRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := fileRegisterAndLogin(t, h, "other@example.com", "password123")
	vaultID := fileCreateVault(t, h, ownerToken, ownerDevice, "Private Vault")

	headers := fileAuthHeaders(otherToken, otherDevice)
	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/changes?since=0", nil, headers)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestFileHandler_FullWorkflow(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")
	headers := fileAuthHeaders(accessToken, deviceID)

	uploadResp := uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/test.md", []byte("initial content"))
	assert.Equal(t, float64(1), uploadResp["version"])

	rec := doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files?path=notes/test.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []byte("initial content"), rec.Body.Bytes())

	encDelta := base64.StdEncoding.EncodeToString([]byte("delta-v1-to-v2"))
	rec = fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      "notes/test.md",
		"base_version":   1,
		"checksum":       "sha256:v2hash",
		"size_bytes":     200,
		"encrypted_data": encDelta,
	}, headers)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=notes/test.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	json.NewDecoder(rec.Body).Decode(&info)
	assert.Equal(t, float64(2), info["version"])

	rec = fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/rename", map[string]string{
		"old_path": "notes/test.md",
		"new_path": "notes/renamed.md",
	}, headers)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=notes/test.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)
	json.NewDecoder(rec.Body).Decode(&info)
	assert.Equal(t, true, info["deleted"])

	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=notes/renamed.md", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)
	json.NewDecoder(rec.Body).Decode(&info)
	assert.Equal(t, false, info["deleted"])
	assert.Equal(t, "notes/renamed.md", info["file_path"])

	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/changes?since=0", nil, headers)
	require.Equal(t, http.StatusOK, rec.Code)
	var events []map[string]any
	json.NewDecoder(rec.Body).Decode(&events)
	assert.GreaterOrEqual(t, len(events), 3)

	eventTypes := make([]string, 0, len(events))
	for _, e := range events {
		eventTypes = append(eventTypes, e["event_type"].(string))
	}
	assert.Contains(t, eventTypes, "file_created")
	assert.Contains(t, eventTypes, "file_updated")
	assert.Contains(t, eventTypes, "file_renamed")
}

func uploadDelta(t *testing.T, h *fileTestHarness, accessToken, deviceID, vaultID, filePath string, baseVersion int, deltaData []byte) map[string]any {
	t.Helper()
	encDelta := base64.StdEncoding.EncodeToString(deltaData)
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path":      filePath,
		"base_version":   baseVersion,
		"checksum":       "sha256:delta" + strconv.Itoa(baseVersion),
		"size_bytes":     100,
		"encrypted_data": encDelta,
	}, fileAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp
}

func TestFileHandler_UploadDelta_NeedsSnapshotInResponse(t *testing.T) {
	h := newFileTestHarness()
	h.fileUC.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 3,
		MaxDeltaSizeRatio:       0,
	})
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("initial content"))

	resp := uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 1, []byte("delta-1"))
	assert.Nil(t, resp["needs_snapshot"])

	resp = uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 2, []byte("delta-2"))
	assert.Nil(t, resp["needs_snapshot"])

	resp = uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 3, []byte("delta-3"))
	assert.Equal(t, true, resp["needs_snapshot"])
}

func TestFileHandler_UploadSnapshot_NoNeedsSnapshotField(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	resp := uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("content"))
	assert.Nil(t, resp["needs_snapshot"])
}

func TestFileHandler_UploadDelta_NeedsSnapshotResetsAfterSnapshot(t *testing.T) {
	h := newFileTestHarness()
	h.fileUC.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 2,
		MaxDeltaSizeRatio:       0,
	})
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	resp := uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 1, []byte("delta-1"))
	assert.Nil(t, resp["needs_snapshot"])

	resp = uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 2, []byte("delta-2"))
	assert.Equal(t, true, resp["needs_snapshot"])

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", []byte("v4-snapshot"))

	resp = uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 4, []byte("delta-after-snap"))
	assert.Nil(t, resp["needs_snapshot"])
}

func TestFileHandler_UploadDelta_NeedsSnapshotBySizeRatio(t *testing.T) {
	h := newFileTestHarness()
	h.fileUC.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 0,
		MaxDeltaSizeRatio:       0.5,
	})
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	snapshotData := make([]byte, 100)
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/hello.md", snapshotData)

	smallDelta := make([]byte, 20)
	resp := uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 1, smallDelta)
	assert.Nil(t, resp["needs_snapshot"])

	largeDelta := make([]byte, 40)
	resp = uploadDelta(t, h, accessToken, deviceID, vaultID, "notes/hello.md", 2, largeDelta)
	assert.Equal(t, true, resp["needs_snapshot"])
}

func TestFileHandler_MultiUserAccess(t *testing.T) {
	h := newFileTestHarness()
	ownerToken, ownerDevice := fileRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := fileRegisterAndLogin(t, h, "editor@example.com", "password123")
	vaultID := fileCreateVault(t, h, ownerToken, ownerDevice, "Shared Vault")

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("editor-vault-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "editor@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, fileAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var invite map[string]any
	json.NewDecoder(rec.Body).Decode(&invite)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, fileAuthHeaders(editorToken, editorDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	uploadSnapshot(t, h, ownerToken, ownerDevice, vaultID, "notes/shared.md", []byte("owner content"))

	editorHeaders := fileAuthHeaders(editorToken, editorDevice)
	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files?path=notes/shared.md", nil, editorHeaders)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []byte("owner content"), rec.Body.Bytes())

	uploadSnapshot(t, h, editorToken, editorDevice, vaultID, "notes/shared.md", []byte("editor update"))

	ownerHeaders := fileAuthHeaders(ownerToken, ownerDevice)
	rec = doRawRequest(h, "GET", "/sync/v1/vaults/"+vaultID+"/files/info?path=notes/shared.md", nil, ownerHeaders)
	require.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	json.NewDecoder(rec.Body).Decode(&info)
	assert.Equal(t, float64(2), info["version"])
}

func TestFileHandler_UploadSnapshot_FileTooLarge(t *testing.T) {
	h := newFileTestHarness()
	h.fileUC.SetMaxFileSize(50)
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	data := make([]byte, 100)
	headers := fileAuthHeaders(accessToken, deviceID)
	headers["X-File-Path"] = "notes/large.md"
	headers["X-Local-Hash"] = fileContentChecksum(data)
	headers["X-Content-Type"] = "text/markdown"
	headers["Content-Length"] = strconv.Itoa(len(data))

	rec := doRawRequest(h, "POST", "/sync/v1/vaults/"+vaultID+"/files", bytes.NewReader(data), headers)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestFileHandler_BulkGetFileInfo_ReturnsMetadata(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/b.md", []byte("content b"))

	body := map[string]any{"file_paths": []string{"notes/a.md", "notes/b.md"}}
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/bulk", body, fileAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, 2, len(resp))
}

func TestFileHandler_BulkGetFileInfo_OmitsMissingFiles(t *testing.T) {
	h := newFileTestHarness()
	accessToken, deviceID := fileRegisterAndLogin(t, h, "user@example.com", "password123")
	vaultID := fileCreateVault(t, h, accessToken, deviceID, "Test Vault")

	uploadSnapshot(t, h, accessToken, deviceID, vaultID, "notes/exists.md", []byte("content"))

	body := map[string]any{"file_paths": []string{"notes/exists.md", "notes/missing.md"}}
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/bulk", body, fileAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, 1, len(resp))
	assert.Equal(t, "notes/exists.md", resp[0]["file_path"])
}

func TestFileHandler_BulkGetFileInfo_Unauthorized(t *testing.T) {
	h := newFileTestHarness()
	vaultID := uuid.New().String()

	body := map[string]any{"file_paths": []string{"notes/a.md"}}
	rec := fileDoJSON(h, "POST", "/sync/v1/vaults/"+vaultID+"/files/bulk", body, map[string]string{})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
