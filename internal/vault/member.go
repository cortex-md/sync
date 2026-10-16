package vault

import (
	"context"
	"errors"

	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type MemberService struct {
	members       VaultMemberRepository
	keys          VaultKeyRepository
	users         UserRepository
	access        AccessRepository
	tx            Transactor
	authz         *authz.Authorizer
	bus           bus.Bus
	guestsEnabled bool
}

type MemberDeps struct {
	Members       VaultMemberRepository
	Keys          VaultKeyRepository
	Users         UserRepository
	Access        AccessRepository
	Tx            Transactor
	Bus           bus.Bus
	Authz         *authz.Authorizer
	GuestsEnabled bool
}

func NewMemberService(d MemberDeps) *MemberService {
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &MemberService{
		members:       d.Members,
		keys:          d.Keys,
		users:         d.Users,
		access:        d.Access,
		tx:            d.Tx,
		bus:           d.Bus,
		authz:         authorizer,
		guestsEnabled: d.GuestsEnabled,
	}
}

type MemberInfo struct {
	VaultID     uuid.UUID
	UserID      uuid.UUID
	Email       string
	DisplayName string
	AvatarURL   string
	PublicKey   []byte
	HasKey      bool
	Role        domain.VaultRole
	JoinedAt    string
}

func (uc *MemberService) List(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]MemberInfo, error) {
	if _, err := uc.authz.Require(ctx, vaultID, actorID, domain.PermMemberRead); err != nil {
		return nil, err
	}

	members, err := uc.members.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	users, err := uc.users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*domain.User, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}

	keys, err := uc.keys.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	hasKey := make(map[uuid.UUID]bool, len(keys))
	for _, k := range keys {
		hasKey[k.UserID] = true
	}

	result := make([]MemberInfo, 0, len(members))
	for _, m := range members {
		user, ok := byID[m.UserID]
		if !ok {
			continue
		}
		result = append(result, MemberInfo{
			VaultID:     m.VaultID,
			UserID:      m.UserID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL(),
			PublicKey:   user.PublicKey,
			HasKey:      hasKey[m.UserID],
			Role:        m.Role,
			JoinedAt:    m.JoinedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	return result, nil
}

type UpdateMemberRoleInput struct {
	ActorID uuid.UUID
	VaultID uuid.UUID
	UserID  uuid.UUID
	Role    domain.VaultRole
}

func (uc *MemberService) UpdateRole(ctx context.Context, input UpdateMemberRoleInput) error {
	if !input.Role.Valid() || input.Role == domain.VaultRoleOwner {
		return domain.ErrInvalidInput
	}

	actor, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermMemberManage)
	if err != nil {
		return err
	}

	target, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		return err
	}

	if !actor.Role.CanAssign(target.Role, input.Role) {
		return domain.ErrInsufficientRole
	}
	if input.Role == domain.VaultRoleGuest && target.Role != domain.VaultRoleGuest && !uc.guestsEnabled {
		return domain.ErrFeatureDisabled
	}

	if err := uc.members.UpdateRole(ctx, input.VaultID, input.UserID, input.Role); err != nil {
		return err
	}

	uc.accessChanged(ctx, input.VaultID, input.UserID)
	bus.PublishVaultEvent(ctx, uc.bus, input.VaultID, bus.VaultEventMemberRoleChanged, map[string]any{
		"user_id": input.UserID.String(),
		"role":    string(input.Role),
	})
	return nil
}

type RemoveMemberInput struct {
	ActorID uuid.UUID
	VaultID uuid.UUID
	UserID  uuid.UUID
}

func (uc *MemberService) Remove(ctx context.Context, input RemoveMemberInput) error {
	if input.ActorID == input.UserID {
		self, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermFileRead)
		if err != nil {
			return err
		}
		if self.Role == domain.VaultRoleOwner {
			return domain.ErrInvalidInput
		}
		return uc.remove(ctx, input.VaultID, input.UserID)
	}

	actor, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermMemberManage)
	if err != nil {
		return err
	}

	target, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		return err
	}

	if !actor.Role.Outranks(target.Role) {
		return domain.ErrInsufficientRole
	}

	return uc.remove(ctx, input.VaultID, input.UserID)
}

func (uc *MemberService) remove(ctx context.Context, vaultID, userID uuid.UUID) error {
	if err := runInTx(ctx, uc.tx, func(ctx context.Context) error {
		if uc.access != nil {
			if err := uc.access.RemovePrincipal(ctx, vaultID, domain.PrincipalUser, userID); err != nil {
				return err
			}
		}
		if err := uc.members.Remove(ctx, vaultID, userID); err != nil {
			return err
		}
		if err := uc.keys.Delete(ctx, vaultID, userID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	uc.accessChanged(ctx, vaultID, userID)
	bus.PublishVaultEvent(ctx, uc.bus, vaultID, bus.VaultEventMemberRemoved, map[string]any{
		"user_id": userID.String(),
	})
	return nil
}

func (uc *MemberService) accessChanged(ctx context.Context, vaultID, userID uuid.UUID) {
	ctx = context.WithoutCancel(ctx)
	uc.authz.InvalidateVault(vaultID)
	bus.PublishAccessChanged(ctx, uc.bus, vaultID, userID)
	bus.PublishVaultAccessChanged(ctx, uc.bus, vaultID)
}
