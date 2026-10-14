package template

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	templates *Service
}

func NewHandler(templates *Service) *Handler {
	return &Handler{templates: templates}
}

type templateResponse struct {
	ID             string  `json:"id"`
	VaultID        string  `json:"vault_id"`
	Kind           string  `json:"kind"`
	Audience       string  `json:"audience"`
	GroupID        *string `json:"group_id"`
	GroupName      *string `json:"group_name"`
	OwnerID        *string `json:"owner_id"`
	OwnerName      string  `json:"owner_name"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	IncludeSamples bool    `json:"include_samples"`
	Encrypted      bool    `json:"encrypted"`
	Version        int     `json:"version"`
	FileCount      int     `json:"file_count"`
	Status         string  `json:"status"`
	ReviewNote     string  `json:"review_note"`
	Slug           string  `json:"slug"`
	URL            *string `json:"url"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

func toTemplateResponse(v *View) templateResponse {
	resp := templateResponse{
		ID:             v.ID.String(),
		VaultID:        v.VaultID.String(),
		Kind:           string(v.Kind),
		Audience:       string(v.Audience),
		GroupName:      v.GroupName,
		OwnerName:      v.OwnerName,
		Name:           v.Name,
		Description:    v.Description,
		IncludeSamples: v.IncludeSamples,
		Encrypted:      v.Encrypted,
		Version:        v.Version,
		FileCount:      v.FileCount,
		Status:         string(v.Status),
		ReviewNote:     v.ReviewNote,
		Slug:           v.Slug,
		CreatedAt:      v.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      v.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if v.GroupID != nil {
		id := v.GroupID.String()
		resp.GroupID = &id
	}
	if v.OwnerID != nil {
		id := v.OwnerID.String()
		resp.OwnerID = &id
	}
	if v.Audience.Public() {
		url := "/t/" + v.Slug
		resp.URL = &url
	}
	return resp
}

type publicTemplateResponse struct {
	Slug        string `json:"slug"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerName   string `json:"owner_name"`
	Version     int    `json:"version"`
	UpdatedAt   string `json:"updated_at"`
	FileCount   int    `json:"file_count"`
}

func toPublicResponse(v *PublicView) publicTemplateResponse {
	return publicTemplateResponse{
		Slug:        v.Slug,
		Kind:        string(v.Kind),
		Name:        v.Name,
		Description: v.Description,
		OwnerName:   v.OwnerName,
		Version:     v.Version,
		UpdatedAt:   v.UpdatedAt.UTC().Format(time.RFC3339),
		FileCount:   v.FileCount,
	}
}

func vaultParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return uuid.Nil, false
	}
	return vaultID, true
}

func templateParams(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	templateID, err := uuid.Parse(chi.URLParam(r, "templateID"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return uuid.Nil, uuid.Nil, false
	}
	return vaultID, templateID, true
}

func decodePayload(w http.ResponseWriter, encoded string) ([]byte, bool) {
	if base64.StdEncoding.DecodedLen(len(encoded)) > MaxPayloadBytes+2 {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "template payload is too large")
		return nil, false
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "payload must be base64")
		return nil, false
	}
	return payload, true
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	views, err := h.templates.List(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID)
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	resp := make([]templateResponse, 0, len(views))
	for i := range views {
		resp = append(resp, toTemplateResponse(&views[i]))
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type createTemplateRequest struct {
	Kind           string  `json:"kind"`
	Audience       string  `json:"audience"`
	GroupID        *string `json:"group_id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	IncludeSamples bool    `json:"include_samples"`
	Payload        string  `json:"payload"`
	Encrypted      bool    `json:"encrypted"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultParam(w, r)
	if !ok {
		return
	}
	var req createTemplateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	groupID, ok := parseGroupID(w, req.GroupID)
	if !ok {
		return
	}
	payload, ok := decodePayload(w, req.Payload)
	if !ok {
		return
	}
	view, err := h.templates.Create(r.Context(), CreateInput{
		UserID:         httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID:        vaultID,
		Kind:           domain.TemplateKind(req.Kind),
		Audience:       domain.TemplateAudience(req.Audience),
		GroupID:        groupID,
		Name:           req.Name,
		Description:    req.Description,
		IncludeSamples: req.IncludeSamples,
		Payload:        payload,
		Encrypted:      req.Encrypted,
	})
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toTemplateResponse(view))
}

type payloadResponse struct {
	Payload   string `json:"payload"`
	Encrypted bool   `json:"encrypted"`
	Version   int    `json:"version"`
}

func (h *Handler) GetPayload(w http.ResponseWriter, r *http.Request) {
	vaultID, templateID, ok := templateParams(w, r)
	if !ok {
		return
	}
	payload, err := h.templates.GetPayload(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID, templateID)
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payloadResponse{
		Payload:   base64.StdEncoding.EncodeToString(payload.Data),
		Encrypted: payload.Encrypted,
		Version:   payload.Version,
	})
}

type replacePayloadRequest struct {
	Payload   string `json:"payload"`
	Encrypted bool   `json:"encrypted"`
}

func (h *Handler) ReplacePayload(w http.ResponseWriter, r *http.Request) {
	vaultID, templateID, ok := templateParams(w, r)
	if !ok {
		return
	}
	var req replacePayloadRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	payload, ok := decodePayload(w, req.Payload)
	if !ok {
		return
	}
	view, err := h.templates.ReplacePayload(r.Context(), PayloadInput{
		UserID:     httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID:    vaultID,
		TemplateID: templateID,
		Payload:    payload,
		Encrypted:  req.Encrypted,
	})
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toTemplateResponse(view))
}

type optionalString struct {
	Set   bool
	Value *string
}

func (o *optionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(data, []byte("null")) {
		o.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type updateTemplateRequest struct {
	Audience       *string        `json:"audience"`
	GroupID        optionalString `json:"group_id"`
	Name           *string        `json:"name"`
	Description    *string        `json:"description"`
	IncludeSamples *bool          `json:"include_samples"`
	Payload        *string        `json:"payload"`
	Encrypted      *bool          `json:"encrypted"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	vaultID, templateID, ok := templateParams(w, r)
	if !ok {
		return
	}
	var req updateTemplateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	input := UpdateInput{
		UserID:         httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID:        vaultID,
		TemplateID:     templateID,
		Name:           req.Name,
		Description:    req.Description,
		IncludeSamples: req.IncludeSamples,
		Encrypted:      req.Encrypted,
		GroupSet:       req.GroupID.Set,
	}
	if req.Audience != nil {
		audience := domain.TemplateAudience(*req.Audience)
		input.Audience = &audience
	}
	if req.GroupID.Set {
		if input.GroupID, ok = parseGroupID(w, req.GroupID.Value); !ok {
			return
		}
	}
	if req.Payload != nil {
		payload, ok := decodePayload(w, *req.Payload)
		if !ok {
			return
		}
		input.Payload = &payload
	}
	view, err := h.templates.Update(r.Context(), input)
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toTemplateResponse(view))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	vaultID, templateID, ok := templateParams(w, r)
	if !ok {
		return
	}
	if err := h.templates.Delete(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID, templateID); err != nil {
		handleTemplateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Market(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	views, err := h.templates.Market(r.Context(), domain.TemplateKind(query.Get("kind")), query.Get("q"))
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	resp := make([]publicTemplateResponse, 0, len(views))
	for i := range views {
		resp = append(resp, toPublicResponse(&views[i]))
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type publicPayloadResponse struct {
	Template publicTemplateResponse `json:"template"`
	Payload  string                 `json:"payload"`
}

func (h *Handler) Public(w http.ResponseWriter, r *http.Request) {
	view, err := h.templates.Resolve(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		handleTemplateError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	httpx.WriteJSON(w, http.StatusOK, publicPayloadResponse{
		Template: toPublicResponse(view),
		Payload:  base64.StdEncoding.EncodeToString(view.Payload),
	})
}

func parseGroupID(w http.ResponseWriter, raw *string) (*uuid.UUID, bool) {
	if raw == nil {
		return nil, true
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid group id")
		return nil, false
	}
	return &id, true
}

func handleTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteError(w, http.StatusBadRequest, "invalid template request")
	case errors.Is(err, domain.ErrVaultAccessDenied), errors.Is(err, domain.ErrInsufficientRole):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "you cannot manage this template", "insufficient_role")
	case errors.Is(err, domain.ErrPublicLinksDisabled):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "public links are disabled for this vault", "public_links_disabled")
	case errors.Is(err, domain.ErrFileTooLarge):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "template payload is too large")
	default:
		log.Error().Err(err).Msg("template request failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
