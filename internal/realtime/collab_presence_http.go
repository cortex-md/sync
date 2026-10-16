package realtime

import (
	"context"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *CollabHandler) peersFromPresence(ctx context.Context, presence []domain.CollabPresence, excludeClientID string) ([]collabPeer, error) {
	ids := make([]uuid.UUID, 0, len(presence))
	for _, p := range presence {
		ids = append(ids, p.UserID)
	}
	users, err := h.users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*domain.User, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}

	peers := make([]collabPeer, 0, len(presence))
	for _, p := range presence {
		if p.ClientID == excludeClientID {
			continue
		}
		if user, ok := byID[p.UserID]; ok {
			peers = append(peers, peerFromUser(p.ClientID, user, p.CanWrite))
		}
	}
	return peers, nil
}

func (h *CollabHandler) GetPeers(w http.ResponseWriter, r *http.Request) {
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

	if _, err := h.authz.RequirePath(r.Context(), vaultID, claims.UserID, filePath, domain.PermFileRead); err != nil {
		writeCollabAccessError(w, err)
		return
	}

	presence, err := h.presence.ListRoom(r.Context(), vaultID, filePath)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}
	peers, err := h.peersFromPresence(r.Context(), presence, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}

	peerIDs := make([]string, 0, len(peers))
	for _, p := range peers {
		peerIDs = append(peerIDs, p.ClientID)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"vault_id":   vaultID.String(),
		"file_path":  filePath,
		"peer_count": len(peers),
		"peer_ids":   peerIDs,
		"peers":      peers,
	})
}

type filePresence struct {
	FilePath string       `json:"file_path"`
	Peers    []collabPeer `json:"peers"`
}

func (h *CollabHandler) GetVaultPresence(w http.ResponseWriter, r *http.Request) {
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

	access, err := h.authz.Access(r.Context(), vaultID, claims.UserID, domain.PermFileRead)
	if err != nil {
		writeCollabAccessError(w, err)
		return
	}

	all, err := h.presence.ListVault(r.Context(), vaultID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}
	presence := all[:0]
	for _, p := range all {
		if access.Scope.CanRead(p.FilePath) {
			presence = append(presence, p)
		}
	}
	peers, err := h.peersFromPresence(r.Context(), presence, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}

	pathByClient := make(map[string]string, len(presence))
	for _, p := range presence {
		pathByClient[p.ClientID] = p.FilePath
	}
	result := []filePresence{}
	index := make(map[string]int)
	for _, peer := range peers {
		path := pathByClient[peer.ClientID]
		i, ok := index[path]
		if !ok {
			i = len(result)
			index[path] = i
			result = append(result, filePresence{FilePath: path})
		}
		result[i].Peers = append(result[i].Peers, peer)
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
