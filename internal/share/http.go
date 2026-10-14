package share

import (
	"embed"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/pages"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

//go:embed viewer.html
var viewerHTML string

//go:embed assets
var assetsFS embed.FS

var viewerPage = template.Must(template.New("viewer").Parse(viewerHTML))

const viewerCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; frame-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

type Handler struct {
	shares    *Service
	publicURL string
}

func NewHandler(shares *Service, publicURL string) *Handler {
	return &Handler{shares: shares, publicURL: publicURL}
}

type shareResponse struct {
	ID         string  `json:"id"`
	VaultID    string  `json:"vault_id"`
	FilePath   string  `json:"file_path"`
	URL        string  `json:"url"`
	WrappedKey string  `json:"wrapped_key"`
	Revision   int     `json:"revision"`
	SizeBytes  int64   `json:"size_bytes"`
	ExpiresAt  *string `json:"expires_at"`
	CreatedBy  string  `json:"created_by"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

func (h *Handler) toResponse(s *domain.NoteShare) shareResponse {
	resp := shareResponse{
		ID:         s.ID,
		VaultID:    s.VaultID.String(),
		FilePath:   s.FilePath,
		URL:        h.publicURL + "/s/" + s.ID,
		WrappedKey: base64.StdEncoding.EncodeToString(s.WrappedKey),
		Revision:   s.Revision,
		SizeBytes:  s.SizeBytes,
		CreatedBy:  s.CreatedBy.String(),
		CreatedAt:  s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:  s.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if s.ExpiresAt != nil {
		expires := s.ExpiresAt.UTC().Format(time.RFC3339)
		resp.ExpiresAt = &expires
	}
	return resp
}

func vaultParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return uuid.Nil, false
	}
	return vaultID, true
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	shares, err := h.shares.List(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID)
	if err != nil {
		handleShareError(w, err)
		return
	}
	resp := make([]shareResponse, 0, len(shares))
	for i := range shares {
		resp = append(resp, h.toResponse(&shares[i]))
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type createShareRequest struct {
	FilePath   string     `json:"file_path"`
	WrappedKey string     `json:"wrapped_key"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	var req createShareRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedKey)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "wrapped_key must be base64")
		return
	}
	share, created, err := h.shares.Create(r.Context(), CreateInput{
		UserID:     httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID:    vaultID,
		FilePath:   req.FilePath,
		WrappedKey: wrapped,
		ExpiresAt:  req.ExpiresAt,
	})
	if err != nil {
		handleShareError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, h.toResponse(share))
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	share, err := h.shares.Publish(r.Context(), PublishInput{
		UserID:  httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID: vaultID,
		ShareID: chi.URLParam(r, "shareID"),
		Size:    r.ContentLength,
		Body:    r.Body,
	})
	if err != nil {
		handleShareError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toResponse(share))
}

type updateShareRequest struct {
	ExpiresAt *time.Time `json:"expires_at"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	var req updateShareRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	share, err := h.shares.UpdateExpiry(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID, chi.URLParam(r, "shareID"), req.ExpiresAt)
	if err != nil {
		handleShareError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toResponse(share))
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	if err := h.shares.Revoke(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID, chi.URLParam(r, "shareID")); err != nil {
		handleShareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Content(w http.ResponseWriter, r *http.Request) {
	share, err := h.shares.Resolve(r.Context(), chi.URLParam(r, "shareID"))
	if err != nil {
		handleShareError(w, err)
		return
	}
	etag := `"r` + strconv.Itoa(share.Revision) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Share-Revision", strconv.Itoa(share.Revision))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, err := h.shares.Open(r.Context(), share)
	if err != nil {
		handleShareError(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(share.SizeBytes, 10))
	if _, err := io.Copy(w, body); err != nil {
		log.Debug().Err(err).Msg("share: content stream aborted")
	}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	shareID := chi.URLParam(r, "shareID")
	share, err := h.shares.Resolve(r.Context(), shareID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			pages.Notice(w, r, http.StatusNotFound, "share.unavailable", "share.invalid")
			return
		}
		pages.Error(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", viewerCSP)
	data := struct{ ShareID, Lang, UpdatedAt string }{shareID, pages.Locale(r), share.UpdatedAt.UTC().Format(time.RFC3339)}
	if err := viewerPage.Execute(w, data); err != nil {
		log.Error().Err(err).Msg("share: failed to render viewer")
	}
}

func Assets() http.Handler {
	assets, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/share-assets/", http.FileServerFS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}

func handleShareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteError(w, http.StatusBadRequest, "invalid share request")
	case errors.Is(err, domain.ErrVaultAccessDenied), errors.Is(err, domain.ErrInsufficientRole):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "you cannot share this note", "insufficient_role")
	case errors.Is(err, domain.ErrPublicLinksDisabled):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "public links are disabled for this vault", "public_links_disabled")
	case errors.Is(err, domain.ErrFileTooLarge):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "shared note is too large")
	case errors.Is(err, domain.ErrQuotaExceeded):
		httpx.WriteErrorWithCode(w, http.StatusInsufficientStorage, "storage quota exceeded", "quota_exceeded")
	default:
		log.Error().Err(err).Msg("share request failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
