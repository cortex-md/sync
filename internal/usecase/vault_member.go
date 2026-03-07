package usecase

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type VaultMemberUsecase struct {
	members port.VaultMemberRepository
	keys    port.VaultKeyRepository
	users   port.UserRepository
}

func NewVaultMemberUsecase(
	members port.VaultMemberRepository,
	keys port.VaultKeyRepository,
	users port.UserRepository,
) *VaultMemberUsecase {
	return &VaultMemberUsecase{
		members: members,
		keys:    keys,
		users:   users,
	}
}

type MemberInfo struct {
	VaultID     uuid.UUID
	UserID      uuid.UUID
	Email       string
	DisplayName string
	Role        domain.VaultRole
	JoinedAt    string
}

func (uc *VaultMemberUsecase) List(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]MemberInfo, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	members, err := uc.members.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	result := make([]MemberInfo, 0, len(members))
	for _, m := range members {
		user, err := uc.users.GetByID(ctx, m.UserID)
		if err != nil {
			continue
		}
		result = append(result, MemberInfo{
			VaultID:     m.VaultID,
			UserID:      m.UserID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
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

func (uc *VaultMemberUsecase) UpdateRole(ctx context.Context, input UpdateMemberRoleInput) error {
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

	target, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		return err
	}

	if target.Role == domain.VaultRoleOwner {
		return domain.ErrInsufficientRole
	}

	if input.Role == domain.VaultRoleOwner {
		return domain.ErrInvalidInput
	}

	if actor.Role == domain.VaultRoleAdmin && input.Role == domain.VaultRoleAdmin && target.Role != domain.VaultRoleAdmin {
		return domain.ErrInsufficientRole
	}

	return uc.members.UpdateRole(ctx, input.VaultID, input.UserID, input.Role)
}

type RemoveMemberInput struct {
	ActorID uuid.UUID
	VaultID uuid.UUID
	UserID  uuid.UUID
}

func (uc *VaultMemberUsecase) Remove(ctx context.Context, input RemoveMemberInput) error {
	actor, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.ActorID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrVaultAccessDenied
		}
		return err
	}

	if input.ActorID == input.UserID {
		if actor.Role == domain.VaultRoleOwner {
			return domain.ErrInvalidInput
		}
		if err := uc.members.Remove(ctx, input.VaultID, input.UserID); err != nil {
			return err
		}
		_ = uc.keys.Delete(ctx, input.VaultID, input.UserID)
		return nil
	}

	if !actor.Role.CanManageMembers() {
		return domain.ErrInsufficientRole
	}

	target, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		return err
	}

	if target.Role == domain.VaultRoleOwner {
		return domain.ErrInsufficientRole
	}

	if actor.Role == domain.VaultRoleAdmin && target.Role == domain.VaultRoleAdmin {
		return domain.ErrInsufficientRole
	}

	if err := uc.members.Remove(ctx, input.VaultID, input.UserID); err != nil {
		return err
	}

	_ = uc.keys.Delete(ctx, input.VaultID, input.UserID)
	return nil
}
