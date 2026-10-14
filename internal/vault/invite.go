package vault

import (
	"context"
	"errors"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/secret"
	"github.com/google/uuid"
)

const inviteTTL = 7 * 24 * time.Hour

type InviteService struct {
	invites       VaultInviteRepository
	members       VaultMemberRepository
	keys          VaultKeyRepository
	users         UserRepository
	vaults        VaultRepository
	access        AccessRepository
	tx            Transactor
	authz         *authz.Authorizer
	outbox        EmailOutboxRepository
	bus           bus.Bus
	publicURL     string
	guestsEnabled bool
}

type InviteDeps struct {
	Invites       VaultInviteRepository
	Members       VaultMemberRepository
	Keys          VaultKeyRepository
	Users         UserRepository
	Vaults        VaultRepository
	Access        AccessRepository
	Tx            Transactor
	Outbox        EmailOutboxRepository
	Bus           bus.Bus
	Authz         *authz.Authorizer
	PublicURL     string
	GuestsEnabled bool
}

func NewInviteService(d InviteDeps) *InviteService {
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &InviteService{
		invites:       d.Invites,
		members:       d.Members,
		keys:          d.Keys,
		users:         d.Users,
		vaults:        d.Vaults,
		access:        d.Access,
		tx:            d.Tx,
		outbox:        d.Outbox,
		bus:           d.Bus,
		publicURL:     d.PublicURL,
		authz:         authorizer,
		guestsEnabled: d.GuestsEnabled,
	}
}

type CreateInviteInput struct {
	ID                uuid.UUID
	ActorID           uuid.UUID
	VaultID           uuid.UUID
	InviteeEmail      string
	Role              domain.VaultRole
	Grants            []GrantInput
	EncryptedVaultKey []byte
}

type InviteInfo struct {
	ID                uuid.UUID
	VaultID           uuid.UUID
	VaultName         string
	InviterID         uuid.UUID
	InviterName       string
	InviteeEmail      string
	Role              domain.VaultRole
	Grants            []domain.InviteGrant
	EncryptedVaultKey []byte
	Accepted          bool
	Token             string
	URL               string
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

func (uc *InviteService) Create(ctx context.Context, input CreateInviteInput) (*InviteInfo, error) {
	input.InviteeEmail = domain.NormalizeEmail(input.InviteeEmail)
	if !domain.ValidEmail(input.InviteeEmail) || !input.Role.Valid() || input.Role == domain.VaultRoleOwner {
		return nil, domain.ErrInvalidInput
	}

	actor, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermInviteManage)
	if err != nil {
		return nil, err
	}
	if !actor.Role.Outranks(input.Role) && actor.Role != domain.VaultRoleOwner {
		return nil, domain.ErrInsufficientRole
	}

	vault, err := uc.vaults.GetByID(ctx, input.VaultID)
	if err != nil {
		return nil, err
	}
	if !emailDomainAllowed(vault.AllowedEmailDomains, input.InviteeEmail) {
		return nil, domain.ErrDomainNotAllowed
	}

	var inviteGrants []domain.InviteGrant
	if input.Role == domain.VaultRoleGuest {
		if !uc.guestsEnabled || !vault.AllowGuests {
			return nil, domain.ErrFeatureDisabled
		}
		grants, err := normalizeInviteGrants(input.Grants)
		if err != nil {
			return nil, err
		}
		inviteGrants = grants
	} else if len(input.Grants) > 0 {
		return nil, domain.ErrInvalidInput
	}

	if input.ID != uuid.Nil {
		if existing, err := uc.invites.GetByID(ctx, input.ID); err == nil {
			return uc.replayCreate(ctx, existing, input, vault)
		}
	}

	invitee, err := uc.users.GetByEmail(ctx, input.InviteeEmail)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if invitee != nil {
		if _, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, invitee.ID); err == nil {
			return nil, domain.ErrAlreadyExists
		}
	}

	pending, err := uc.invites.PendingExists(ctx, input.VaultID, input.InviteeEmail)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, domain.ErrAlreadyExists
	}

	inviter, err := uc.users.GetByID(ctx, input.ActorID)
	if err != nil {
		return nil, err
	}

	rawToken, tokenHash, err := secret.New()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	invite := &domain.VaultInvite{
		ID:                input.ID,
		VaultID:           input.VaultID,
		InviterID:         input.ActorID,
		InviteeEmail:      input.InviteeEmail,
		Role:              input.Role,
		EncryptedVaultKey: input.EncryptedVaultKey,
		TokenHash:         tokenHash,
		Grants:            inviteGrants,
		ExpiresAt:         now.Add(inviteTTL),
		CreatedAt:         now,
	}
	if invite.ID == uuid.Nil {
		invite.ID = uuid.New()
	}

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.invites.Create(ctx, invite); err != nil {
			return err
		}
		locale := inviter.Locale
		if invitee != nil {
			locale = invitee.Locale
		}
		return uc.enqueueInviteEmail(ctx, invite, vault, inviter, locale, rawToken)
	}); err != nil {
		return nil, err
	}

	info := toInviteInfo(invite, vault.Name)
	info.InviterName = inviter.DisplayNameOrEmail()
	info.Token = rawToken
	info.URL = uc.inviteURL(rawToken)
	return info, nil
}

func (uc *InviteService) replayCreate(ctx context.Context, existing *domain.VaultInvite, input CreateInviteInput, vault *domain.Vault) (*InviteInfo, error) {
	if existing.VaultID != input.VaultID || existing.InviterID != input.ActorID ||
		existing.InviteeEmail != input.InviteeEmail || existing.Role != input.Role {
		return nil, domain.ErrAlreadyExists
	}
	if existing.Accepted {
		return nil, domain.ErrAlreadyExists
	}
	if time.Now().After(existing.ExpiresAt) {
		return nil, domain.ErrInviteExpired
	}
	rawToken, tokenHash, err := secret.New()
	if err != nil {
		return nil, err
	}
	inviter, err := uc.users.GetByID(ctx, input.ActorID)
	if err != nil {
		return nil, err
	}
	locale := inviter.Locale
	if invitee, err := uc.users.GetByEmail(ctx, existing.InviteeEmail); err == nil {
		locale = invitee.Locale
	}
	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.invites.RotateToken(ctx, existing.ID, tokenHash); err != nil {
			return err
		}
		return uc.enqueueInviteEmail(ctx, existing, vault, inviter, locale, rawToken)
	}); err != nil {
		return nil, err
	}
	info := toInviteInfo(existing, vault.Name)
	info.InviterName = inviter.DisplayNameOrEmail()
	info.Token = rawToken
	info.URL = uc.inviteURL(rawToken)
	return info, nil
}

func (uc *InviteService) enqueueInviteEmail(ctx context.Context, invite *domain.VaultInvite, vault *domain.Vault, inviter *domain.User, locale string, rawToken string) error {
	if uc.outbox == nil {
		return nil
	}
	return uc.outbox.Enqueue(ctx, &domain.OutboxEmail{
		To:       invite.InviteeEmail,
		Template: domain.EmailTemplateVaultInvite,
		Locale:   locale,
		Data: map[string]string{
			"InviterName": inviter.DisplayNameOrEmail(),
			"VaultName":   vault.Name,
			"Role":        string(invite.Role),
			"Link":        uc.inviteURL(rawToken),
			"ExpiresAt":   invite.ExpiresAt.UTC().Format("2006-01-02"),
		},
	})
}

func (uc *InviteService) inviteURL(rawToken string) string {
	return uc.publicURL + "/invite/" + rawToken
}

func (uc *InviteService) ListByVault(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]InviteInfo, error) {
	if _, err := uc.authz.Require(ctx, vaultID, actorID, domain.PermInviteManage); err != nil {
		return nil, err
	}

	vault, err := uc.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	invites, err := uc.invites.ListPendingByVault(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	result := make([]InviteInfo, 0, len(invites))
	for i := range invites {
		info := toInviteInfo(&invites[i], vault.Name)
		info.EncryptedVaultKey = nil
		result = append(result, *info)
	}
	return result, nil
}

func (uc *InviteService) ListMyInvites(ctx context.Context, userEmail string) ([]InviteInfo, error) {
	details, err := uc.invites.ListPendingForEmail(ctx, domain.NormalizeEmail(userEmail))
	if err != nil {
		return nil, err
	}

	result := make([]InviteInfo, 0, len(details))
	for i := range details {
		info := toInviteInfo(&details[i].VaultInvite, details[i].VaultName)
		info.InviterName = details[i].InviterName
		if info.InviterName == "" {
			info.InviterName = details[i].InviterEmail
		}
		result = append(result, *info)
	}
	return result, nil
}

type InvitePreview struct {
	VaultName   string
	InviterName string
	Role        domain.VaultRole
	ExpiresAt   time.Time
	MemberCount int
}

func (uc *InviteService) Preview(ctx context.Context, rawToken string) (*InvitePreview, error) {
	invite, err := uc.usableInviteByToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	vault, err := uc.vaults.GetByID(ctx, invite.VaultID)
	if err != nil {
		return nil, err
	}
	inviterName := ""
	if inviter, err := uc.users.GetByID(ctx, invite.InviterID); err == nil {
		inviterName = inviter.DisplayNameOrEmail()
	}
	members, err := uc.members.ListByVaultID(ctx, invite.VaultID)
	if err != nil {
		return nil, err
	}
	return &InvitePreview{
		VaultName:   vault.Name,
		InviterName: inviterName,
		Role:        invite.Role,
		ExpiresAt:   invite.ExpiresAt,
		MemberCount: len(members),
	}, nil
}

type DeclinedInvite struct {
	InviterName string
}

func (uc *InviteService) Decline(ctx context.Context, rawToken string) (*DeclinedInvite, error) {
	invite, err := uc.usableInviteByToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	vault, err := uc.vaults.GetByID(ctx, invite.VaultID)
	if err != nil {
		return nil, err
	}
	inviter, err := uc.users.GetByID(ctx, invite.InviterID)
	if err != nil {
		return nil, err
	}
	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.invites.Delete(ctx, invite.ID); err != nil {
			return err
		}
		if uc.outbox == nil {
			return nil
		}
		return uc.outbox.Enqueue(ctx, &domain.OutboxEmail{
			To:       inviter.Email,
			Template: domain.EmailTemplateInviteDeclined,
			Locale:   inviter.Locale,
			Data: map[string]string{
				"DisplayName":  inviter.DisplayNameOrEmail(),
				"InviteeEmail": invite.InviteeEmail,
				"VaultName":    vault.Name,
			},
		})
	}); err != nil {
		return nil, err
	}
	return &DeclinedInvite{InviterName: inviter.DisplayNameOrEmail()}, nil
}

type AcceptInviteInput struct {
	UserID   uuid.UUID
	InviteID uuid.UUID
	Token    string
}

type AcceptedInvite struct {
	Vault             *VaultInfo
	EncryptedVaultKey []byte
}

func (uc *InviteService) Accept(ctx context.Context, input AcceptInviteInput) (*AcceptedInvite, error) {
	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return nil, err
	}

	var result *AcceptedInvite
	entriesAdded := false
	err = uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		invite, txErr := uc.resolveInvite(ctx, input, user)
		if txErr != nil {
			return txErr
		}

		vault, txErr := uc.vaults.GetByID(ctx, invite.VaultID)
		if txErr != nil {
			return txErr
		}
		if !emailDomainAllowed(vault.AllowedEmailDomains, domain.NormalizeEmail(user.Email)) {
			return domain.ErrDomainNotAllowed
		}
		if invite.Role == domain.VaultRoleGuest && (!uc.guestsEnabled || !vault.AllowGuests) {
			return domain.ErrFeatureDisabled
		}

		now := time.Now()
		if txErr = uc.members.Add(ctx, &domain.VaultMember{
			VaultID:  invite.VaultID,
			UserID:   input.UserID,
			Role:     invite.Role,
			JoinedAt: now,
		}); txErr != nil {
			return txErr
		}

		if invite.Role == domain.VaultRoleGuest && uc.access != nil {
			for _, g := range invite.Grants {
				level := domain.AccessView
				if g.CanWrite {
					level = domain.AccessEdit
				}
				if txErr = uc.access.UpsertUserEntry(ctx, invite.VaultID, g.Path, input.UserID, level, invite.InviterID); txErr != nil {
					return txErr
				}
				entriesAdded = true
			}
		}

		if txErr = uc.invites.Accept(ctx, invite.ID); txErr != nil {
			return txErr
		}

		result = &AcceptedInvite{
			Vault:             toVaultInfo(vault, invite.Role),
			EncryptedVaultKey: invite.EncryptedVaultKey,
		}

		bus.PublishVaultEvent(ctx, uc.bus, invite.VaultID, bus.VaultEventMemberJoined, map[string]any{
			"user_id": input.UserID.String(),
			"role":    string(invite.Role),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if entriesAdded {
		uc.authz.InvalidateVault(result.Vault.ID)
		bus.PublishVaultAccessChanged(ctx, uc.bus, result.Vault.ID)
	}
	if info, infoErr := loadVaultInfo(ctx, uc.vaults, result.Vault.ID, input.UserID); infoErr == nil {
		result.Vault = info
	}
	return result, nil
}

func (uc *InviteService) resolveInvite(ctx context.Context, input AcceptInviteInput, user *domain.User) (*domain.VaultInvite, error) {
	if input.Token != "" {
		return uc.usableInviteByToken(ctx, input.Token)
	}
	invite, err := uc.invites.GetByID(ctx, input.InviteID)
	if err != nil {
		return nil, err
	}
	if domain.NormalizeEmail(invite.InviteeEmail) != domain.NormalizeEmail(user.Email) {
		return nil, domain.ErrVaultAccessDenied
	}
	if invite.Accepted {
		return nil, domain.ErrAlreadyExists
	}
	if time.Now().After(invite.ExpiresAt) {
		return nil, domain.ErrInviteExpired
	}
	return invite, nil
}

type GrantInput struct {
	Path     string
	CanWrite bool
}

const maxGrantsPerGuest = 100

func normalizeInviteGrants(inputs []GrantInput) ([]domain.InviteGrant, error) {
	if len(inputs) == 0 || len(inputs) > maxGrantsPerGuest {
		return nil, domain.ErrInvalidInput
	}
	grants := make([]domain.InviteGrant, 0, len(inputs))
	index := make(map[string]int, len(inputs))
	for _, in := range inputs {
		path, ok := domain.NormalizeGrantPath(in.Path)
		if !ok {
			return nil, domain.ErrInvalidInput
		}
		if i, seen := index[path]; seen {
			grants[i].CanWrite = grants[i].CanWrite || in.CanWrite
			continue
		}
		index[path] = len(grants)
		grants = append(grants, domain.InviteGrant{Path: path, CanWrite: in.CanWrite})
	}
	return grants, nil
}

func (uc *InviteService) usableInviteByToken(ctx context.Context, rawToken string) (*domain.VaultInvite, error) {
	if rawToken == "" {
		return nil, domain.ErrNotFound
	}
	invite, err := uc.invites.GetByTokenHash(ctx, secret.Hash(rawToken))
	if err != nil {
		return nil, err
	}
	if invite.Accepted {
		return nil, domain.ErrAlreadyExists
	}
	if time.Now().After(invite.ExpiresAt) {
		return nil, domain.ErrInviteExpired
	}
	return invite, nil
}

type DeleteInviteInput struct {
	ActorID  uuid.UUID
	VaultID  uuid.UUID
	InviteID uuid.UUID
}

func (uc *InviteService) Delete(ctx context.Context, input DeleteInviteInput) error {
	if _, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermInviteManage); err != nil {
		return err
	}

	invite, err := uc.invites.GetByID(ctx, input.InviteID)
	if err != nil {
		return err
	}
	if invite.VaultID != input.VaultID {
		return domain.ErrNotFound
	}
	return uc.invites.Delete(ctx, input.InviteID)
}

func toInviteInfo(inv *domain.VaultInvite, vaultName string) *InviteInfo {
	return &InviteInfo{
		ID:                inv.ID,
		VaultID:           inv.VaultID,
		VaultName:         vaultName,
		InviterID:         inv.InviterID,
		InviteeEmail:      inv.InviteeEmail,
		Role:              inv.Role,
		Grants:            inv.Grants,
		EncryptedVaultKey: inv.EncryptedVaultKey,
		Accepted:          inv.Accepted,
		ExpiresAt:         inv.ExpiresAt,
		CreatedAt:         inv.CreatedAt,
	}
}
