package vault

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

const x25519PublicKeySize = 32

type KeyService struct {
	keys    VaultKeyRepository
	members VaultMemberRepository
	users   UserRepository
	authz   *authz.Authorizer
	bus     bus.Bus
}

type KeyDeps struct {
	Keys    VaultKeyRepository
	Members VaultMemberRepository
	Users   UserRepository
	Bus     bus.Bus
	Authz   *authz.Authorizer
}

func NewKeyService(d KeyDeps) *KeyService {
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &KeyService{
		keys:    d.Keys,
		members: d.Members,
		users:   d.Users,
		bus:     d.Bus,
		authz:   authorizer,
	}
}

type VaultKeyInfo struct {
	VaultID      uuid.UUID
	UserID       uuid.UUID
	EncryptedKey []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type UpsertVaultKeyInput struct {
	ActorID      uuid.UUID
	VaultID      uuid.UUID
	EncryptedKey []byte
}

func (uc *KeyService) Upsert(ctx context.Context, input UpsertVaultKeyInput) (*VaultKeyInfo, error) {
	if len(input.EncryptedKey) == 0 {
		return nil, domain.ErrInvalidInput
	}

	if _, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermFileRead); err != nil {
		return nil, err
	}

	return uc.store(ctx, input.VaultID, input.ActorID, input.EncryptedKey)
}

type GrantVaultKeyInput struct {
	ActorID      uuid.UUID
	VaultID      uuid.UUID
	UserID       uuid.UUID
	EncryptedKey []byte
}

func (uc *KeyService) Grant(ctx context.Context, input GrantVaultKeyInput) (*VaultKeyInfo, error) {
	if len(input.EncryptedKey) == 0 {
		return nil, domain.ErrInvalidInput
	}

	actor, err := uc.authz.Require(ctx, input.VaultID, input.ActorID, domain.PermKeyGrant)
	if err != nil {
		return nil, err
	}

	target, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		return nil, err
	}

	if _, err := uc.keys.GetByVaultAndUser(ctx, input.VaultID, input.UserID); err == nil {
		if input.UserID == input.ActorID || !actor.Role.Outranks(target.Role) {
			return nil, domain.ErrKeyExists
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	info, err := uc.store(ctx, input.VaultID, input.UserID, input.EncryptedKey)
	if err != nil {
		return nil, err
	}

	bus.PublishVaultEvent(ctx, uc.bus, input.VaultID, bus.VaultEventKeyGranted, map[string]any{
		"user_id": input.UserID.String(),
	})
	return info, nil
}

func (uc *KeyService) store(ctx context.Context, vaultID, userID uuid.UUID, encryptedKey []byte) (*VaultKeyInfo, error) {
	now := time.Now()
	key := &domain.VaultKey{
		VaultID:      vaultID,
		UserID:       userID,
		EncryptedKey: encryptedKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := uc.keys.Upsert(ctx, key); err != nil {
		return nil, err
	}

	return toVaultKeyInfo(key), nil
}

func (uc *KeyService) Get(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) (*VaultKeyInfo, error) {
	if _, err := uc.authz.Require(ctx, vaultID, actorID, domain.PermFileRead); err != nil {
		return nil, err
	}

	key, err := uc.keys.GetByVaultAndUser(ctx, vaultID, actorID)
	if err != nil {
		return nil, err
	}

	return toVaultKeyInfo(key), nil
}

func (uc *KeyService) ListByVault(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]VaultKeyInfo, error) {
	if _, err := uc.authz.Require(ctx, vaultID, actorID, domain.PermKeyGrant); err != nil {
		return nil, err
	}

	keys, err := uc.keys.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}

	result := make([]VaultKeyInfo, 0, len(keys))
	for i := range keys {
		result = append(result, *toVaultKeyInfo(&keys[i]))
	}
	return result, nil
}

type PendingKeyMember struct {
	UserID      uuid.UUID
	Role        domain.VaultRole
	PublicKey   []byte
	DisplayName string
	Email       string
	AvatarURL   string
}

func (uc *KeyService) ListPending(ctx context.Context, actorID uuid.UUID, vaultID uuid.UUID) ([]PendingKeyMember, error) {
	if _, err := uc.authz.Require(ctx, vaultID, actorID, domain.PermKeyGrant); err != nil {
		return nil, err
	}

	members, err := uc.members.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	keys, err := uc.keys.ListByVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	hasKey := make(map[uuid.UUID]bool, len(keys))
	for _, k := range keys {
		hasKey[k.UserID] = true
	}

	var missing []uuid.UUID
	roles := make(map[uuid.UUID]domain.VaultRole)
	for _, m := range members {
		if !hasKey[m.UserID] {
			missing = append(missing, m.UserID)
			roles[m.UserID] = m.Role
		}
	}
	if len(missing) == 0 || uc.users == nil {
		return []PendingKeyMember{}, nil
	}

	users, err := uc.users.ListByIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	result := make([]PendingKeyMember, 0, len(users))
	for _, u := range users {
		result = append(result, PendingKeyMember{
			UserID: u.ID, Role: roles[u.ID], PublicKey: u.PublicKey,
			DisplayName: u.DisplayName, Email: u.Email, AvatarURL: u.AvatarURL(),
		})
	}
	return result, nil
}

func (uc *KeyService) SetPublicKey(ctx context.Context, userID uuid.UUID, publicKey []byte) error {
	if len(publicKey) != x25519PublicKeySize || uc.users == nil {
		return domain.ErrInvalidInput
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.KDFVersion == domain.KDFVersionArgon2 {
		if bytes.Equal(user.PublicKey, publicKey) {
			return nil
		}
		return domain.ErrAlreadyExists
	}
	return uc.users.UpdatePublicKey(ctx, userID, publicKey)
}

func (uc *KeyService) GetPublicKey(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	if uc.users == nil {
		return nil, domain.ErrNotFound
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(user.PublicKey) == 0 {
		return nil, domain.ErrNotFound
	}
	return user.PublicKey, nil
}

func toVaultKeyInfo(key *domain.VaultKey) *VaultKeyInfo {
	return &VaultKeyInfo{
		VaultID:      key.VaultID,
		UserID:       key.UserID,
		EncryptedKey: key.EncryptedKey,
		CreatedAt:    key.CreatedAt,
		UpdatedAt:    key.UpdatedAt,
	}
}
