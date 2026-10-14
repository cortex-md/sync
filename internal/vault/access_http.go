package vault

import (
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type AccessHandler struct {
	access *AccessService
}

func NewAccessHandler(access *AccessService) *AccessHandler {
	return &AccessHandler{access: access}
}

type GroupResponse struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
	CreatedAt string   `json:"created_at"`
}

func ToGroupResponse(g domain.VaultGroup) GroupResponse {
	ids := make([]string, 0, len(g.MemberIDs))
	for _, id := range g.MemberIDs {
		ids = append(ids, id.String())
	}
	return GroupResponse{ID: g.ID.String(), Name: g.Name, MemberIDs: ids, CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339)}
}

func ToGroupResponses(groups []domain.VaultGroup) []GroupResponse {
	resp := make([]GroupResponse, 0, len(groups))
	for _, g := range groups {
		resp = append(resp, ToGroupResponse(g))
	}
	return resp
}

type AccessEntryPayload struct {
	PrincipalType string `json:"principal_type"`
	PrincipalID   string `json:"principal_id"`
	Level         string `json:"level"`
}

type AccessRuleResponse struct {
	Path       string               `json:"path"`
	Restricted bool                 `json:"restricted"`
	Entries    []AccessEntryPayload `json:"entries"`
}

func ToAccessRuleResponse(rule domain.PathRule) AccessRuleResponse {
	entries := make([]AccessEntryPayload, 0, len(rule.Entries))
	for _, e := range rule.Entries {
		entries = append(entries, AccessEntryPayload{
			PrincipalType: string(e.PrincipalType),
			PrincipalID:   e.PrincipalID.String(),
			Level:         e.Level.String(),
		})
	}
	return AccessRuleResponse{Path: rule.Path, Restricted: rule.Restricted, Entries: entries}
}

func ToAccessRuleResponses(rules []domain.PathRule) []AccessRuleResponse {
	resp := make([]AccessRuleResponse, 0, len(rules))
	for _, r := range rules {
		resp = append(resp, ToAccessRuleResponse(r))
	}
	return resp
}

type EffectiveRuleResponse struct {
	Path  string `json:"path"`
	Level string `json:"level"`
}

type EffectiveAccessResponse struct {
	All   bool                    `json:"all"`
	Base  string                  `json:"base"`
	Rules []EffectiveRuleResponse `json:"rules"`
}

func ToEffectiveAccessResponse(scope domain.Scope) EffectiveAccessResponse {
	resp := EffectiveAccessResponse{All: scope.All, Base: scope.Base.String(), Rules: []EffectiveRuleResponse{}}
	if scope.All {
		return resp
	}
	for _, r := range scope.SortedRules() {
		resp.Rules = append(resp.Rules, EffectiveRuleResponse{Path: r.Path, Level: r.Level.String()})
	}
	return resp
}

func (h *AccessHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	groups, err := h.access.ListGroups(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToGroupResponses(groups))
}

type createGroupRequest struct {
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
}

func (h *AccessHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	var req createGroupRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ids, ok := parseUUIDs(w, req.MemberIDs)
	if !ok {
		return
	}
	group, err := h.access.CreateGroup(r.Context(), CreateGroupInput{
		ActorID: httpx.ClaimsFromContext(r.Context()).UserID, VaultID: vaultID, Name: req.Name, MemberIDs: ids,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ToGroupResponse(*group))
}

type updateGroupRequest struct {
	Name      *string   `json:"name"`
	MemberIDs *[]string `json:"member_ids"`
}

func (h *AccessHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	vaultID, groupID, ok := vaultAndGroupParams(w, r)
	if !ok {
		return
	}
	var req updateGroupRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	input := UpdateGroupInput{ActorID: httpx.ClaimsFromContext(r.Context()).UserID, VaultID: vaultID, GroupID: groupID, Name: req.Name}
	if req.MemberIDs != nil {
		ids, ok := parseUUIDs(w, *req.MemberIDs)
		if !ok {
			return
		}
		input.MemberIDs = &ids
	}
	group, err := h.access.UpdateGroup(r.Context(), input)
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToGroupResponse(*group))
}

func (h *AccessHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	vaultID, groupID, ok := vaultAndGroupParams(w, r)
	if !ok {
		return
	}
	if err := h.access.DeleteGroup(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID, groupID); err != nil {
		handleVaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AccessHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	rules, err := h.access.ListRules(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToAccessRuleResponses(rules))
}

type putRuleRequest struct {
	Path       string               `json:"path"`
	Restricted bool                 `json:"restricted"`
	Entries    []AccessEntryPayload `json:"entries"`
}

func (h *AccessHandler) PutRule(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	var req putRuleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	entries := make([]AccessEntryInput, 0, len(req.Entries))
	for _, e := range req.Entries {
		id, err := uuid.Parse(e.PrincipalID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid principal_id")
			return
		}
		entries = append(entries, AccessEntryInput{PrincipalType: e.PrincipalType, PrincipalID: id, Level: e.Level})
	}
	rule, err := h.access.PutRule(r.Context(), PutRuleInput{
		ActorID: httpx.ClaimsFromContext(r.Context()).UserID, VaultID: vaultID,
		Path: req.Path, Restricted: req.Restricted, Entries: entries,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}
	if rule == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToAccessRuleResponse(*rule))
}

func vaultAndGroupParams(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "groupID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid group id")
		return uuid.Nil, uuid.Nil, false
	}
	return vaultID, groupID, true
}

func parseUUIDs(w http.ResponseWriter, values []string) ([]uuid.UUID, bool) {
	ids := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid member id")
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}
