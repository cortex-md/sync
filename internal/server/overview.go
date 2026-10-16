package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/account"
	"github.com/cortexnotes/cortex-sync/internal/device"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/subscription"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type OverviewHandler struct {
	accounts      *account.Service
	devices       *device.Service
	vaults        *vault.Service
	members       *vault.MemberService
	invites       *vault.InviteService
	access        *vault.AccessService
	subscriptions *subscription.Service
	authz         *authz.Authorizer
	deviceStore   DeviceRepository
	memberStore   VaultMemberRepository
	presence      CollabPresenceRepository
}

type vaultCursorResponse struct {
	VaultID     string `json:"vault_id"`
	LastEventID int64  `json:"last_event_id"`
	SyncedAt    string `json:"synced_at"`
}

type overviewDevice struct {
	device.DeviceResponse
	VaultCursors []vaultCursorResponse `json:"vault_cursors"`
}

type accountOverviewResponse struct {
	Account      account.AccountResponse      `json:"account"`
	Devices      []overviewDevice             `json:"devices"`
	Vaults       []vault.VaultResponse        `json:"vaults"`
	Invites      []vault.InviteResponse       `json:"invites"`
	Subscription *subscription.StatusResponse `json:"subscription,omitempty"`
}

func (h *OverviewHandler) Account(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := httpx.ClaimsFromContext(ctx)

	user, err := h.accounts.Me(ctx, claims.UserID)
	if err != nil {
		overviewError(w, err)
		return
	}
	devices, err := h.devices.List(ctx, claims.UserID, claims.DeviceID)
	if err != nil {
		overviewError(w, err)
		return
	}
	cursors, err := h.deviceStore.ListVaultCursorsByUser(ctx, claims.UserID)
	if err != nil {
		overviewError(w, err)
		return
	}
	vaults, err := h.vaults.List(ctx, claims.UserID)
	if err != nil {
		overviewError(w, err)
		return
	}
	invites, err := h.invites.ListMyInvites(ctx, claims.UserID)
	if err != nil {
		overviewError(w, err)
		return
	}

	cursorsByDevice := make(map[uuid.UUID][]vaultCursorResponse)
	for _, c := range cursors {
		cursorsByDevice[c.DeviceID] = append(cursorsByDevice[c.DeviceID], vaultCursorResponse{
			VaultID:     c.VaultID.String(),
			LastEventID: c.LastEventID,
			SyncedAt:    c.SyncedAt.UTC().Format(time.RFC3339),
		})
	}

	resp := accountOverviewResponse{
		Account: account.ToAccountResponse(user),
		Devices: make([]overviewDevice, 0, len(devices)),
		Vaults:  make([]vault.VaultResponse, 0, len(vaults)),
		Invites: make([]vault.InviteResponse, 0, len(invites)),
	}
	for _, d := range devices {
		entry := overviewDevice{DeviceResponse: device.ToDeviceResponse(d), VaultCursors: cursorsByDevice[d.ID]}
		if entry.VaultCursors == nil {
			entry.VaultCursors = []vaultCursorResponse{}
		}
		resp.Devices = append(resp.Devices, entry)
	}
	for _, v := range vaults {
		resp.Vaults = append(resp.Vaults, vault.ToVaultResponse(v))
	}
	for _, inv := range invites {
		resp.Invites = append(resp.Invites, vault.ToInviteResponse(inv))
	}
	if h.subscriptions != nil {
		status := subscription.StatusOf(h.statusFor(ctx, claims.UserID))
		resp.Subscription = &status
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *OverviewHandler) statusFor(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	return h.subscriptions.GetStatus(ctx, userID)
}

type memberDevice struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	DeviceName string `json:"device_name"`
	DeviceType string `json:"device_type"`
	LastSeenAt string `json:"last_seen_at"`
	SyncedAt   string `json:"synced_at,omitempty"`
}

type presencePeer struct {
	UserID   string `json:"user_id"`
	FilePath string `json:"file_path"`
	CanWrite bool   `json:"can_write"`
}

type vaultOverviewResponse struct {
	Vault       vault.VaultResponse           `json:"vault"`
	MyRole      string                        `json:"my_role"`
	MyAccess    vault.EffectiveAccessResponse `json:"my_access"`
	Members     []vault.MemberResponse        `json:"members"`
	Groups      []vault.GroupResponse         `json:"groups"`
	AccessRules []vault.AccessRuleResponse    `json:"access_rules,omitempty"`
	Invites     []vault.InviteResponse        `json:"invites,omitempty"`
	Devices     []memberDevice                `json:"devices"`
	Presence    []presencePeer                `json:"presence"`
}

func (h *OverviewHandler) Vault(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := httpx.ClaimsFromContext(ctx)
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	access, err := h.authz.Access(ctx, vaultID, claims.UserID, domain.PermFileRead)
	if err != nil {
		overviewError(w, err)
		return
	}
	info, err := h.vaults.Get(ctx, claims.UserID, vaultID)
	if err != nil {
		overviewError(w, err)
		return
	}

	resp := vaultOverviewResponse{
		Vault:    vault.ToVaultResponse(*info),
		MyRole:   string(access.Member.Role),
		MyAccess: vault.ToEffectiveAccessResponse(access.Scope),
		Members:  []vault.MemberResponse{},
		Groups:   []vault.GroupResponse{},
		Devices:  []memberDevice{},
		Presence: []presencePeer{},
	}

	role := access.Member.Role
	if role.Can(domain.PermMemberRead) {
		members, err := h.members.List(ctx, claims.UserID, vaultID)
		if err != nil {
			overviewError(w, err)
			return
		}
		userIDs := make([]uuid.UUID, 0, len(members))
		for _, m := range members {
			resp.Members = append(resp.Members, vault.ToMemberResponse(m))
			userIDs = append(userIDs, m.UserID)
		}
		if err := h.attachDevices(ctx, vaultID, userIDs, role.Can(domain.PermMemberManage), &resp); err != nil {
			overviewError(w, err)
			return
		}
		groups, err := h.access.ListGroups(ctx, claims.UserID, vaultID)
		if err != nil {
			overviewError(w, err)
			return
		}
		resp.Groups = vault.ToGroupResponses(groups)
	}

	if role.Can(domain.PermMemberManage) {
		rules, err := h.access.ListRules(ctx, claims.UserID, vaultID)
		if err != nil {
			overviewError(w, err)
			return
		}
		resp.AccessRules = vault.ToAccessRuleResponses(rules)
	}

	if role.Can(domain.PermInviteManage) {
		invites, err := h.invites.ListByVault(ctx, claims.UserID, vaultID)
		if err != nil {
			overviewError(w, err)
			return
		}
		resp.Invites = make([]vault.InviteResponse, 0, len(invites))
		for _, inv := range invites {
			if !inv.Accepted && time.Now().Before(inv.ExpiresAt) {
				resp.Invites = append(resp.Invites, vault.ToInviteResponse(inv))
			}
		}
	}

	if h.presence != nil {
		peers, err := h.presence.ListVault(ctx, vaultID)
		if err != nil {
			overviewError(w, err)
			return
		}
		for _, p := range peers {
			if access.Scope.CanRead(p.FilePath) {
				resp.Presence = append(resp.Presence, presencePeer{UserID: p.UserID.String(), FilePath: p.FilePath, CanWrite: p.CanWrite})
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *OverviewHandler) attachDevices(ctx context.Context, vaultID uuid.UUID, userIDs []uuid.UUID, allDevices bool, resp *vaultOverviewResponse) error {
	devices, err := h.deviceStore.ListActiveByUsers(ctx, userIDs)
	if err != nil {
		return err
	}
	cursors, err := h.deviceStore.ListVaultCursorsByVault(ctx, vaultID)
	if err != nil {
		return err
	}
	syncedAt := make(map[uuid.UUID]time.Time, len(cursors))
	for _, c := range cursors {
		syncedAt[c.DeviceID] = c.SyncedAt
	}
	for _, d := range devices {
		entry := memberDevice{
			ID:         d.ID.String(),
			UserID:     d.UserID.String(),
			DeviceName: d.DeviceName,
			DeviceType: d.DeviceType,
			LastSeenAt: d.LastSeenAt.UTC().Format(time.RFC3339),
		}
		t, synced := syncedAt[d.ID]
		if !synced && !allDevices {
			continue
		}
		if synced {
			entry.SyncedAt = t.UTC().Format(time.RFC3339)
		}
		resp.Devices = append(resp.Devices, entry)
	}
	return nil
}

func overviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrVaultAccessDenied), errors.Is(err, domain.ErrInsufficientRole):
		httpx.WriteError(w, http.StatusForbidden, "vault access denied")
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	default:
		log.Error().Err(err).Msg("overview request failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
