package usecase

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type VaultInviteUsecase struct {
	invites port.VaultInviteRepository
	members port.VaultMemberRepository
	keys    port.VaultKeyRepository
	users   port.UserRepository
	vaults  port.VaultRepository
	tx      port.Transactor
}

func NewVaultInviteUsecase(
	invites port.VaultInviteRepository,
	members port.VaultMemberRepository,
	keys port.VaultKeyRepository,
	users port.UserRepository,
	vaults port.VaultRepository,
	tx port.Transactor,
) *VaultInviteUsecase {
	return &VaultInviteUsecase{
		invites: invites,
		members: members,
		keys:    keys,
		users:   users,
		vaults:  vaults,
		tx:      tx,
	}
}

type CreateInviteInput struct {
	ActorID           uuid.UUID
	VaultID           uuid.UUID
	InviteeEmail      string
	Role              domain.VaultRole
	EncryptedVaultKey []byte
}

type InviteInfo struct {
	ID                uuid.UUID
	VaultID           uuid.UUID
	VaultName         string
	InviterID         uuid.UUID
	InviteeEmail      string
	Role              domain.VaultRole
	EncryptedVaultKey []byte
	Accepted          bool
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

func (uc *VaultInviteUsecase) Create(ctx context.Context, input CreateInviteInput) (*InviteInfo, error) {
	if input.InviteeEmail == "" {
		return nil, domain.ErrInvalidInput
	}

	if input.Role == domain.VaultRoleOwner {
		return nil, domain.ErrInvalidInput
	}

	if len(input.EncryptedVaultKey) == 0 {
		return nil, domain.ErrInvalidInput
	}

	actor, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.ActorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !actor.Role.CanManageMembers() {
		return nil, domain.ErrInsufficientRole
	}

	invitee, err := uc.users.GetByEmail(ctx, input.InviteeEmail)
	if err != nil && err != domain.ErrNotFound {
		return nil, err
	}

	if invitee != nil {
		_, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, invitee.ID)
		if err == nil {
			return nil, domain.ErrAlreadyExists
		}
	}

	vault, err := uc.vaults.GetByID(ctx, input.VaultID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	invite := &domain.VaultInvite{
		ID:                uuid.New(),
		VaultID:           input.VaultID,
		InviterID:         input.ActorID,
		InviteeEmail:      input.InviteeEmail,
		Role:              input.Role,
		EncryptedVaultKey: input.EncryptedVaultKey,
		ExpiresAt:         now.Add(7 * 24 * time.Hour),
		CreatedAt:         now,
	}

	if err := uc.invites.Create(ctx, invite); err != nil {
		return nil, err
	}

	return &InviteInfo{
		ID:                invite.ID,
		VaultID:           invite.VaultID,
		VaultName:         vault.Name,
		InviterID:         invite.InviterID,
		InviteeEmail:      invite.InviteeEmail,
		Role:              invite.Role,
		EncryptedVaultKey: invite.EncryptedVaultKey,
		ExpiresAt:         invite.ExpiresAt,
		CreatedAt:         invite.CreatedAt,
	}, nil
}

func (uc *VaultInviteUsecase) ListByVault(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]InviteInfo, error) {
	actor, err := uc.members.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !actor.Role.CanManageMembers() {
		return nil, domain.ErrInsufficientRole
	}

	vault, err := uc.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	invites, err := uc.invites.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	result := make([]InviteInfo, 0, len(invites))
	for _, inv := range invites {
		result = append(result, InviteInfo{
			ID:           inv.ID,
			VaultID:      inv.VaultID,
			VaultName:    vault.Name,
			InviterID:    inv.InviterID,
			InviteeEmail: inv.InviteeEmail,
			Role:         inv.Role,
			Accepted:     inv.Accepted,
			ExpiresAt:    inv.ExpiresAt,
			CreatedAt:    inv.CreatedAt,
		})
	}

	return result, nil
}

func (uc *VaultInviteUsecase) ListMyInvites(ctx context.Context, userEmail string) ([]InviteInfo, error) {
	invites, err := uc.invites.ListByEmail(ctx, userEmail)
	if err != nil {
		return nil, err
	}

	result := make([]InviteInfo, 0, len(invites))
	for _, inv := range invites {
		if inv.Accepted {
			continue
		}
		if time.Now().After(inv.ExpiresAt) {
			continue
		}
		vaultName := ""
		vault, err := uc.vaults.GetByID(ctx, inv.VaultID)
		if err == nil {
			vaultName = vault.Name
		}
		result = append(result, InviteInfo{
			ID:                inv.ID,
			VaultID:           inv.VaultID,
			VaultName:         vaultName,
			InviterID:         inv.InviterID,
			InviteeEmail:      inv.InviteeEmail,
			Role:              inv.Role,
			EncryptedVaultKey: inv.EncryptedVaultKey,
			ExpiresAt:         inv.ExpiresAt,
			CreatedAt:         inv.CreatedAt,
		})
	}

	return result, nil
}

type AcceptInviteInput struct {
	UserID   uuid.UUID
	InviteID uuid.UUID
}

func (uc *VaultInviteUsecase) Accept(ctx context.Context, input AcceptInviteInput) (*VaultInfo, error) {
	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return nil, err
	}

	var vaultInfo *VaultInfo

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		invite, txErr := uc.invites.GetByID(ctx, input.InviteID)
		if txErr != nil {
			return txErr
		}

		if invite.InviteeEmail != user.Email {
			return domain.ErrVaultAccessDenied
		}

		if invite.Accepted {
			return domain.ErrAlreadyExists
		}

		if time.Now().After(invite.ExpiresAt) {
			return domain.ErrInviteExpired
		}

		now := time.Now()
		member := &domain.VaultMember{
			VaultID:  invite.VaultID,
			UserID:   input.UserID,
			Role:     invite.Role,
			JoinedAt: now,
		}

		if txErr = uc.members.Add(ctx, member); txErr != nil {
			return txErr
		}

		if len(invite.EncryptedVaultKey) > 0 {
			vaultKey := &domain.VaultKey{
				VaultID:      invite.VaultID,
				UserID:       input.UserID,
				EncryptedKey: invite.EncryptedVaultKey,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if txErr = uc.keys.Upsert(ctx, vaultKey); txErr != nil {
				return txErr
			}
		}

		if txErr = uc.invites.Accept(ctx, input.InviteID); txErr != nil {
			return txErr
		}

		vault, txErr := uc.vaults.GetByID(ctx, invite.VaultID)
		if txErr != nil {
			return txErr
		}

		vaultInfo = &VaultInfo{
			ID:          vault.ID,
			Name:        vault.Name,
			Description: vault.Description,
			OwnerID:     vault.OwnerID,
			Role:        invite.Role,
			CreatedAt:   vault.CreatedAt,
			UpdatedAt:   vault.UpdatedAt,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return vaultInfo, nil
}

type DeleteInviteInput struct {
	ActorID  uuid.UUID
	VaultID  uuid.UUID
	InviteID uuid.UUID
}

func (uc *VaultInviteUsecase) Delete(ctx context.Context, input DeleteInviteInput) error {
	actor, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.ActorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrVaultAccessDenied
		}
		return err
	}

	if !actor.Role.CanManageMembers() {
		return domain.ErrInsufficientRole
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
