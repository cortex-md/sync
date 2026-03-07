package handler

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type FileHandler struct {
	files *usecase.FileUsecase
}

func NewFileHandler(files *usecase.FileUsecase) *FileHandler {
	return &FileHandler{files: files}
}

func (h *FileHandler) UploadSnapshot(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := GetDeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.Header.Get("X-File-Path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing X-File-Path header")
		return
	}

	checksum := r.Header.Get("X-Local-Hash")
	contentType := r.Header.Get("X-Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var sizeBytes int64
	if sizeStr := r.Header.Get("Content-Length"); sizeStr != "" {
		sizeBytes, _ = strconv.ParseInt(sizeStr, 10, 64)
	}

	info, err := h.files.UploadSnapshot(r.Context(), usecase.UploadSnapshotInput{
		UserID:      claims.UserID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    filePath,
		Checksum:    checksum,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		Data:        r.Body,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	WriteJSON(w, http.StatusCreated, toFileInfoResponse(info))
}

type uploadDeltaRequest struct {
	FilePath      string `json:"file_path"`
	BaseVersion   int    `json:"base_version"`
	Checksum      string `json:"checksum"`
	SizeBytes     int64  `json:"size_bytes"`
	EncryptedData string `json:"encrypted_data"`
}

func (h *FileHandler) UploadDelta(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := GetDeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req uploadDeltaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	encryptedData, err := base64.StdEncoding.DecodeString(req.EncryptedData)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid encrypted_data: must be base64 encoded")
		return
	}

	info, err := h.files.UploadDelta(r.Context(), usecase.UploadDeltaInput{
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

	WriteJSON(w, http.StatusCreated, toFileInfoResponse(info))
}

func (h *FileHandler) DownloadSnapshot(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	var version int
	if versionStr := r.URL.Query().Get("version"); versionStr != "" {
		version, err = strconv.Atoi(versionStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid version query parameter")
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
	w.Header().Set("X-File-Version", strconv.Itoa(result.Snapshot.Version))
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

func (h *FileHandler) DownloadDeltas(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	var sinceVersion int
	if sinceStr := r.URL.Query().Get("since_version"); sinceStr != "" {
		sinceVersion, err = strconv.Atoi(sinceStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid since_version query parameter")
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

	WriteJSON(w, http.StatusOK, resp)
}

func (h *FileHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := GetDeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	err = h.files.DeleteFile(r.Context(), usecase.DeleteFileInput{
		UserID:   claims.UserID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: filePath,
	})
	if err != nil {
		handleFileError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type renameFileRequest struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

func (h *FileHandler) RenameFile(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := GetDeviceID(r.Context())

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req renameFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	info, err := h.files.RenameFile(r.Context(), usecase.RenameFileInput{
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

	WriteJSON(w, http.StatusOK, toFileInfoResponse(info))
}

func (h *FileHandler) GetFileInfo(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	info, err := h.files.GetFileInfo(r.Context(), claims.UserID, vaultID, filePath)
	if err != nil {
		handleFileError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, toFileInfoResponse(info))
}

func (h *FileHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	includeDeleted := r.URL.Query().Get("include_deleted") == "true"

	files, err := h.files.ListFiles(r.Context(), claims.UserID, vaultID, includeDeleted)
	if err != nil {
		handleFileError(w, err)
		return
	}

	resp := make([]fileInfoResponse, 0, len(files))
	for _, f := range files {
		resp = append(resp, toFileInfoResponse(&f))
	}

	WriteJSON(w, http.StatusOK, resp)
}

type historyEntryResponse struct {
	SnapshotID string `json:"snapshot_id"`
	Version    int    `json:"version"`
	SizeBytes  int64  `json:"size_bytes"`
	Checksum   string `json:"checksum"`
	CreatedBy  string `json:"created_by"`
	DeviceID   string `json:"device_id"`
	CreatedAt  string `json:"created_at"`
}

func (h *FileHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
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
			CreatedBy:  e.CreatedBy.String(),
			DeviceID:   e.DeviceID.String(),
			CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		})
	}

	WriteJSON(w, http.StatusOK, resp)
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

func (h *FileHandler) ListChanges(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var sinceEventID int64
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		sinceEventID, err = strconv.ParseInt(sinceStr, 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid since query parameter")
			return
		}
	}

	var limit int
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid limit query parameter")
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

	WriteJSON(w, http.StatusOK, resp)
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
	UpdatedAt      string `json:"updated_at"`
	CreatedAt      string `json:"created_at"`
}

func toFileInfoResponse(info *usecase.FileInfo) fileInfoResponse {
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
	return resp
}

type bulkGetFileInfoRequest struct {
	FilePaths []string `json:"file_paths"`
}

func (h *FileHandler) BulkGetFileInfo(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req bulkGetFileInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.FilePaths) == 0 {
		WriteJSON(w, http.StatusOK, []fileInfoResponse{})
		return
	}

	files, err := h.files.BulkGetFileInfo(r.Context(), usecase.BulkGetFileInfoInput{
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

	WriteJSON(w, http.StatusOK, resp)
}

func handleFileError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrInvalidInput:
		WriteError(w, http.StatusBadRequest, err.Error())
	case domain.ErrNotFound:
		WriteError(w, http.StatusNotFound, "not found")
	case domain.ErrVaultAccessDenied:
		WriteError(w, http.StatusForbidden, "vault access denied")
	case domain.ErrInsufficientRole:
		WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
	case domain.ErrConflict:
		WriteErrorWithCode(w, http.StatusConflict, "version conflict", "conflict")
	case domain.ErrAlreadyExists:
		WriteError(w, http.StatusConflict, "already exists")
	case domain.ErrFileTooLarge:
		WriteError(w, http.StatusRequestEntityTooLarge, "file too large")
	default:
		WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
