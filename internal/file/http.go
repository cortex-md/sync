package file

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	files *Service
}

const (
	maxBulkFileInfoPaths = 1000
	uploadReadTimeout    = 10 * time.Minute
	filePathEncodingURI  = "uri"
)

func filePathFromHeader(r *http.Request) (string, error) {
	filePath := r.Header.Get("X-File-Path")
	if r.Header.Get("X-File-Path-Encoding") != filePathEncodingURI {
		return filePath, nil
	}
	return url.PathUnescape(filePath)
}

func NewHandler(files *Service) *Handler {
	return &Handler{files: files}
}

func (h *Handler) UploadSnapshot(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath, err := filePathFromHeader(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid X-File-Path header")
		return
	}
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing X-File-Path header")
		return
	}

	var baseVersion *int
	if raw := r.Header.Get("X-Base-Version"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid X-Base-Version header")
			return
		}
		baseVersion = &parsed
	}

	checksum := r.Header.Get("X-Local-Hash")
	contentType := r.Header.Get("X-Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadReadTimeout))

	info, err := h.files.UploadSnapshot(r.Context(), UploadSnapshotInput{
		UserID:      claims.UserID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    filePath,
		Checksum:    checksum,
		ContentType: contentType,
		SizeBytes:   r.ContentLength,
		BaseVersion: baseVersion,
		Data:        r.Body,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toFileInfoResponse(info))
}

type uploadDeltaRequest struct {
	FilePath      string `json:"file_path"`
	BaseVersion   int    `json:"base_version"`
	Checksum      string `json:"checksum"`
	SizeBytes     int64  `json:"size_bytes"`
	EncryptedData string `json:"encrypted_data"`
}

func (h *Handler) UploadDelta(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req uploadDeltaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	encryptedData, err := base64.StdEncoding.DecodeString(req.EncryptedData)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid encrypted_data: must be base64 encoded")
		return
	}

	info, err := h.files.UploadDelta(r.Context(), UploadDeltaInput{
		UserID:        claims.UserID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      req.FilePath,
		BaseVersion:   req.BaseVersion,
		Checksum:      req.Checksum,
		SizeBytes:     req.SizeBytes,
		EncryptedData: encryptedData,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toFileInfoResponse(info))
}

func (h *Handler) DownloadSnapshot(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	var version int
	if versionStr := r.URL.Query().Get("version"); versionStr != "" {
		version, err = strconv.Atoi(versionStr)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid version query parameter")
			return
		}
	}

	result, err := h.files.DownloadSnapshot(r.Context(), claims.UserID, vaultID, filePath, version)
	if err != nil {
		handleFileError(w, err)
		return
	}
	defer result.Reader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-File-Path", result.Snapshot.FilePath)
	w.Header().Set("X-File-Path-Encoded", url.PathEscape(result.Snapshot.FilePath))
	w.Header().Set("X-File-Version", strconv.Itoa(result.FileVersion))
	w.Header().Set("X-Snapshot-Version", strconv.Itoa(result.Snapshot.Version))
	w.Header().Set("X-Checksum", result.Snapshot.Checksum)
	w.Header().Set("X-Size-Bytes", strconv.FormatInt(result.Snapshot.SizeBytes, 10))
	w.Header().Set("X-Created-By", result.Snapshot.CreatedBy.String())
	w.Header().Set("X-Device-ID", result.Snapshot.DeviceID.String())
	w.Header().Set("X-Snapshot-ID", result.Snapshot.ID.String())
	w.WriteHeader(http.StatusOK)
	io.Copy(w, result.Reader)
}

type deltaResponse struct {
	ID             string `json:"id"`
	FilePath       string `json:"file_path"`
	BaseVersion    int    `json:"base_version"`
	TargetVersion  int    `json:"target_version"`
	EncryptedDelta string `json:"encrypted_delta"`
	SizeBytes      int64  `json:"size_bytes"`
	CreatedBy      string `json:"created_by"`
	DeviceID       string `json:"device_id"`
	CreatedAt      string `json:"created_at"`
}

func (h *Handler) DownloadDeltas(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	var sinceVersion int
	if sinceStr := r.URL.Query().Get("since_version"); sinceStr != "" {
		sinceVersion, err = strconv.Atoi(sinceStr)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid since_version query parameter")
			return
		}
	}

	result, err := h.files.DownloadDeltas(r.Context(), claims.UserID, vaultID, filePath, sinceVersion)
	if err != nil {
		handleFileError(w, err)
		return
	}

	resp := make([]deltaResponse, 0, len(result.Deltas))
	for _, d := range result.Deltas {
		resp = append(resp, deltaResponse{
			ID:             d.ID.String(),
			FilePath:       d.FilePath,
			BaseVersion:    d.BaseVersion,
			TargetVersion:  d.TargetVersion,
			EncryptedDelta: base64.StdEncoding.EncodeToString(d.EncryptedDelta),
			SizeBytes:      d.SizeBytes,
			CreatedBy:      d.CreatedBy.String(),
			DeviceID:       d.DeviceID.String(),
			CreatedAt:      d.CreatedAt.Format(time.RFC3339),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	var baseVersion *int
	if raw := r.Header.Get("X-Base-Version"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid X-Base-Version header")
			return
		}
		baseVersion = &parsed
	}

	err = h.files.DeleteFile(r.Context(), DeleteFileInput{
		UserID:      claims.UserID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    filePath,
		BaseVersion: baseVersion,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) RestoreFile(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	err = h.files.RestoreFile(r.Context(), RestoreFileInput{
		UserID:   claims.UserID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: filePath,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type renameFileRequest struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

func (h *Handler) RenameFile(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req renameFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	info, err := h.files.RenameFile(r.Context(), RenameFileInput{
		UserID:   claims.UserID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  req.OldPath,
		NewPath:  req.NewPath,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toFileInfoResponse(info))
}

func (h *Handler) GetFileInfo(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	info, err := h.files.GetFileInfo(r.Context(), claims.UserID, vaultID, filePath)
	if err != nil {
		handleFileError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toFileInfoResponse(info))
}

func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	query := r.URL.Query()
	limit := 0
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid limit query parameter")
			return
		}
	}
	includeDeleted := query.Get("include_deleted") == "true"
	after := query.Get("cursor")

	if after == "" {
		latest, scopeKey, err := h.files.LatestEventID(r.Context(), claims.UserID, vaultID)
		if err != nil {
			handleFileError(w, err)
			return
		}
		etag := listETag(latest, scopeKey, includeDeleted, limit)
		if r.Header.Get("If-None-Match") == etag {
			w.Header().Set("ETag", etag)
			w.Header().Set("X-Latest-Event-ID", strconv.FormatInt(latest, 10))
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	page, err := h.files.ListFilesPage(r.Context(), ListFilesInput{
		UserID:         claims.UserID,
		VaultID:        vaultID,
		IncludeDeleted: includeDeleted,
		After:          after,
		Limit:          limit,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	resp := make([]fileInfoResponse, 0, len(page.Files))
	for i := range page.Files {
		resp = append(resp, toFileInfoResponse(&page.Files[i]))
	}

	w.Header().Set("X-Latest-Event-ID", strconv.FormatInt(page.LatestEventID, 10))
	if after == "" {
		w.Header().Set("ETag", listETag(page.LatestEventID, page.ScopeKey, includeDeleted, limit))
	}
	if page.NextCursor != "" {
		w.Header().Set("X-Next-Cursor", page.NextCursor)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func listETag(latestEventID int64, scopeKey string, includeDeleted bool, limit int) string {
	return fmt.Sprintf(`W/"files-%d-%s-%t-%d"`, latestEventID, scopeKey, includeDeleted, limit)
}

type historyEntryResponse struct {
	SnapshotID string `json:"snapshot_id"`
	Version    int    `json:"version"`
	SizeBytes  int64  `json:"size_bytes"`
	Checksum   string `json:"checksum"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	AuthorURL  string `json:"author_avatar_url,omitempty"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	CreatedAt  string `json:"created_at"`
}

func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	entries, err := h.files.GetHistory(r.Context(), claims.UserID, vaultID, filePath)
	if err != nil {
		handleFileError(w, err)
		return
	}

	resp := make([]historyEntryResponse, 0, len(entries))
	for _, e := range entries {
		resp = append(resp, historyEntryResponse{
			SnapshotID: e.SnapshotID.String(),
			Version:    e.Version,
			SizeBytes:  e.SizeBytes,
			Checksum:   e.Checksum,
			AuthorID:   e.AuthorID.String(),
			AuthorName: e.AuthorName,
			AuthorURL:  e.AuthorAvatarURL,
			DeviceID:   e.DeviceID.String(),
			DeviceName: e.DeviceName,
			CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

type syncEventResponse struct {
	ID        int64          `json:"id"`
	VaultID   string         `json:"vault_id"`
	EventType string         `json:"event_type"`
	FilePath  string         `json:"file_path"`
	Version   int            `json:"version"`
	ActorID   string         `json:"actor_id"`
	DeviceID  string         `json:"device_id"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt string         `json:"created_at"`
}

func (h *Handler) ListChanges(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var sinceEventID int64
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		sinceEventID, err = strconv.ParseInt(sinceStr, 10, 64)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid since query parameter")
			return
		}
	}

	var limit int
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid limit query parameter")
			return
		}
	}

	result, err := h.files.ListChanges(r.Context(), claims.UserID, vaultID, sinceEventID, limit)
	if err != nil {
		handleFileError(w, err)
		return
	}

	resp := make([]syncEventResponse, 0, len(result.Events))
	for _, e := range result.Events {
		resp = append(resp, syncEventResponse{
			ID:        e.ID,
			VaultID:   e.VaultID.String(),
			EventType: string(e.EventType),
			FilePath:  e.FilePath,
			Version:   e.Version,
			ActorID:   e.ActorID.String(),
			DeviceID:  e.DeviceID.String(),
			Metadata:  e.Metadata,
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

type fileInfoResponse struct {
	VaultID        string `json:"vault_id"`
	FilePath       string `json:"file_path"`
	Version        int    `json:"version"`
	SnapshotID     string `json:"snapshot_id,omitempty"`
	Checksum       string `json:"checksum"`
	SizeBytes      int64  `json:"size_bytes"`
	ContentType    string `json:"content_type"`
	Deleted        bool   `json:"deleted"`
	NeedsSnapshot  bool   `json:"needs_snapshot,omitempty"`
	LastModifiedBy string `json:"last_modified_by"`
	LastDeviceID   string `json:"last_device_id"`
	CreatedBy      string `json:"created_by,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	CreatedAt      string `json:"created_at"`
}

func toFileInfoResponse(info *FileInfo) fileInfoResponse {
	resp := fileInfoResponse{
		VaultID:        info.VaultID.String(),
		FilePath:       info.FilePath,
		Version:        info.Version,
		Checksum:       info.Checksum,
		SizeBytes:      info.SizeBytes,
		ContentType:    info.ContentType,
		Deleted:        info.Deleted,
		NeedsSnapshot:  info.NeedsSnapshot,
		LastModifiedBy: info.LastModifiedBy.String(),
		LastDeviceID:   info.LastDeviceID.String(),
		UpdatedAt:      info.UpdatedAt.Format(time.RFC3339),
		CreatedAt:      info.CreatedAt.Format(time.RFC3339),
	}
	if info.SnapshotID != uuid.Nil {
		resp.SnapshotID = info.SnapshotID.String()
	}
	if info.CreatedBy != uuid.Nil {
		resp.CreatedBy = info.CreatedBy.String()
	}
	return resp
}

type bulkGetFileInfoRequest struct {
	FilePaths []string `json:"file_paths"`
}

func (h *Handler) BulkGetFileInfo(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req bulkGetFileInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.FilePaths) == 0 {
		httpx.WriteJSON(w, http.StatusOK, []fileInfoResponse{})
		return
	}
	if len(req.FilePaths) > maxBulkFileInfoPaths {
		httpx.WriteError(w, http.StatusBadRequest, "too many file_paths")
		return
	}

	files, err := h.files.BulkGetFileInfo(r.Context(), BulkGetFileInfoInput{
		UserID:    claims.UserID,
		VaultID:   vaultID,
		FilePaths: req.FilePaths,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	resp := make([]fileInfoResponse, 0, len(files))
	for i := range files {
		resp = append(resp, toFileInfoResponse(&files[i]))
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func handleFileError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteError(w, http.StatusBadRequest, domain.ErrInvalidInput.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrVaultAccessDenied):
		httpx.WriteError(w, http.StatusForbidden, "vault access denied")
	case errors.Is(err, domain.ErrInsufficientRole):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
	case errors.Is(err, domain.ErrConflict):
		httpx.WriteErrorWithCode(w, http.StatusConflict, "version conflict", "conflict")
	case errors.Is(err, domain.ErrAlreadyExists):
		httpx.WriteError(w, http.StatusConflict, "already exists")
	case errors.Is(err, domain.ErrFileTooLarge):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "file too large")
	case errors.Is(err, domain.ErrQuotaExceeded):
		httpx.WriteErrorWithCode(w, http.StatusInsufficientStorage, "vault storage quota exceeded", "quota_exceeded")
	default:
		log.Error().Err(err).Msg("file request failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
