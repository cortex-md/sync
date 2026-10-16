package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VaultRepository struct{ store }

func NewVaultRepository(pool *pgxpool.Pool) *VaultRepository {
	return &VaultRepository{store{pool}}
}

func toVault(v db.Vault) domain.Vault {
	return domain.Vault{
		ID:                  v.ID,
		Name:                v.Name,
		Description:         v.Description,
		OwnerID:             v.OwnerID,
		Icon:                v.Icon,
		AllowedEmailDomains: v.AllowedEmailDomains,
		AllowGuests:         v.AllowGuests,
		AllowPublicLinks:    v.AllowPublicLinks,
		StorageUsedBytes:    v.StorageUsedBytes,
		StorageQuotaBytes:   v.StorageQuotaBytes,
		CreatedAt:           v.CreatedAt,
		UpdatedAt:           v.UpdatedAt,
	}
}

func emptyIfNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (r *VaultRepository) Create(ctx context.Context, v *domain.Vault) error {
	n, err := r.q(ctx).CreateVault(ctx, db.CreateVaultParams{
		ID:                  v.ID,
		Name:                v.Name,
		Description:         v.Description,
		OwnerID:             v.OwnerID,
		Icon:                v.Icon,
		AllowedEmailDomains: emptyIfNil(v.AllowedEmailDomains),
		AllowGuests:         v.AllowGuests,
		AllowPublicLinks:    v.AllowPublicLinks,
		StorageQuotaBytes:   v.StorageQuotaBytes,
		CreatedAt:           v.CreatedAt,
		UpdatedAt:           v.UpdatedAt,
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrAlreadyExists
	}
	return nil
}

func (r *VaultRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error) {
	v, err := r.q(ctx).GetVault(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	vault := toVault(v)
	return &vault, nil
}

func (r *VaultRepository) ReconcileStorage(ctx context.Context) (int64, error) {
	n, err := r.q(ctx).ReconcileVaultStorage(ctx)
	return n, mapError(err)
}

func (r *VaultRepository) GetForUpdate(ctx context.Context, id uuid.UUID) (*domain.Vault, error) {
	v, err := r.q(ctx).GetVaultForUpdate(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	vault := toVault(v)
	return &vault, nil
}

func (r *VaultRepository) ListSummariesForUser(ctx context.Context, userID uuid.UUID) ([]domain.VaultSummary, error) {
	rows, err := r.q(ctx).ListVaultSummariesForUser(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	summaries := make([]domain.VaultSummary, len(rows))
	for i, row := range rows {
		summaries[i] = domain.VaultSummary{
			Vault: toVault(db.Vault{
				ID: row.ID, Name: row.Name, Description: row.Description, OwnerID: row.OwnerID,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Icon: row.Icon,
				AllowedEmailDomains: row.AllowedEmailDomains, AllowGuests: row.AllowGuests, AllowPublicLinks: row.AllowPublicLinks,
				StorageUsedBytes: row.StorageUsedBytes, StorageQuotaBytes: row.StorageQuotaBytes,
			}),
			Role:        row.Role,
			MemberCount: int(row.MemberCount),
			HasKey:      row.HasKey,
		}
	}
	return summaries, nil
}

func (r *VaultRepository) GetSummaryForUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultSummary, error) {
	row, err := r.q(ctx).GetVaultSummaryForUser(ctx, db.GetVaultSummaryForUserParams{VaultID: vaultID, UserID: userID})
	if err != nil {
		return nil, mapError(err)
	}
	return &domain.VaultSummary{
		Vault: toVault(db.Vault{
			ID: row.ID, Name: row.Name, Description: row.Description, OwnerID: row.OwnerID,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Icon: row.Icon,
			AllowedEmailDomains: row.AllowedEmailDomains, AllowGuests: row.AllowGuests, AllowPublicLinks: row.AllowPublicLinks,
			StorageUsedBytes: row.StorageUsedBytes, StorageQuotaBytes: row.StorageQuotaBytes,
		}),
		Role:        row.Role,
		MemberCount: int(row.MemberCount),
		HasKey:      row.HasKey,
	}, nil
}

func (r *VaultRepository) Update(ctx context.Context, v *domain.Vault) error {
	return expectRows(r.q(ctx).UpdateVault(ctx, db.UpdateVaultParams{
		ID:                  v.ID,
		Name:                v.Name,
		Description:         v.Description,
		Icon:                v.Icon,
		AllowedEmailDomains: emptyIfNil(v.AllowedEmailDomains),
		AllowGuests:         v.AllowGuests,
		AllowPublicLinks:    v.AllowPublicLinks,
		StorageQuotaBytes:   v.StorageQuotaBytes,
	}))
}

func (r *VaultRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return expectRows(r.q(ctx).DeleteVault(ctx, id))
}

func (r *VaultRepository) AddStorageWithinQuota(ctx context.Context, id uuid.UUID, delta int64) error {
	rows, err := r.q(ctx).AddVaultStorageWithinQuota(ctx, db.AddVaultStorageWithinQuotaParams{ID: id, Delta: delta})
	if err != nil {
		return mapError(err)
	}
	if rows == 0 {
		return domain.ErrQuotaExceeded
	}
	return nil
}

func (r *VaultRepository) AddStorage(ctx context.Context, id uuid.UUID, delta int64) error {
	if delta == 0 {
		return nil
	}
	return mapError(r.q(ctx).AddVaultStorage(ctx, db.AddVaultStorageParams{ID: id, Delta: delta}))
}

type VaultMemberRepository struct{ store }

func NewVaultMemberRepository(pool *pgxpool.Pool) *VaultMemberRepository {
	return &VaultMemberRepository{store{pool}}
}

func toMember(m db.VaultMember) domain.VaultMember {
	return domain.VaultMember{VaultID: m.VaultID, UserID: m.UserID, Role: m.Role, JoinedAt: m.JoinedAt}
}

func (r *VaultMemberRepository) Add(ctx context.Context, m *domain.VaultMember) error {
	return mapError(r.q(ctx).AddMember(ctx, db.AddMemberParams{VaultID: m.VaultID, UserID: m.UserID, Role: m.Role, JoinedAt: m.JoinedAt}))
}

func (r *VaultMemberRepository) GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error) {
	m, err := r.q(ctx).GetMember(ctx, db.GetMemberParams{VaultID: vaultID, UserID: userID})
	if err != nil {
		return nil, mapError(err)
	}
	member := toMember(m)
	return &member, nil
}

func (r *VaultMemberRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error) {
	rows, err := r.q(ctx).ListMembers(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	members := make([]domain.VaultMember, len(rows))
	for i, m := range rows {
		members[i] = toMember(m)
	}
	return members, nil
}

func (r *VaultMemberRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.VaultMember, error) {
	rows, err := r.q(ctx).ListMembershipsByUser(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	members := make([]domain.VaultMember, len(rows))
	for i, m := range rows {
		members[i] = toMember(m)
	}
	return members, nil
}

func (r *VaultMemberRepository) UpdateRole(ctx context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error {
	return expectRows(r.q(ctx).UpdateMemberRole(ctx, db.UpdateMemberRoleParams{VaultID: vaultID, UserID: userID, Role: role}))
}

func (r *VaultMemberRepository) Remove(ctx context.Context, vaultID, userID uuid.UUID) error {
	return expectRows(r.q(ctx).RemoveMember(ctx, db.RemoveMemberParams{VaultID: vaultID, UserID: userID}))
}

type VaultInviteRepository struct{ store }

func NewVaultInviteRepository(pool *pgxpool.Pool) *VaultInviteRepository {
	return &VaultInviteRepository{store{pool}}
}

func toInvite(i db.VaultInvite) domain.VaultInvite {
	var grants []domain.InviteGrant
	if len(i.Grants) > 0 {
		_ = json.Unmarshal(i.Grants, &grants)
	}
	invite := domain.VaultInvite{
		ID:                i.ID,
		VaultID:           i.VaultID,
		InviterID:         i.InviterID,
		InviteeEmail:      i.InviteeEmail,
		Role:              i.Role,
		EncryptedVaultKey: i.EncryptedVaultKey,
		Grants:            grants,
		Accepted:          i.Accepted,
		ExpiresAt:         i.ExpiresAt,
		CreatedAt:         i.CreatedAt,
	}
	if i.TokenHash != nil {
		invite.TokenHash = *i.TokenHash
	}
	return invite
}

func (r *VaultInviteRepository) Create(ctx context.Context, inv *domain.VaultInvite) error {
	grants := inv.Grants
	if grants == nil {
		grants = []domain.InviteGrant{}
	}
	encodedGrants, err := json.Marshal(grants)
	if err != nil {
		return err
	}
	if err := r.q(ctx).DeleteExpiredPendingInvite(ctx, db.DeleteExpiredPendingInviteParams{VaultID: inv.VaultID, Email: inv.InviteeEmail}); err != nil {
		return mapError(err)
	}
	n, err := r.q(ctx).CreateInvite(ctx, db.CreateInviteParams{
		ID:                inv.ID,
		VaultID:           inv.VaultID,
		InviterID:         inv.InviterID,
		InviteeEmail:      inv.InviteeEmail,
		Role:              inv.Role,
		EncryptedVaultKey: inv.EncryptedVaultKey,
		TokenHash:         inv.TokenHash,
		Grants:            encodedGrants,
		ExpiresAt:         inv.ExpiresAt,
		CreatedAt:         inv.CreatedAt,
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrAlreadyExists
	}
	return nil
}

func (r *VaultInviteRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.VaultInvite, error) {
	i, err := r.q(ctx).GetInvite(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	invite := toInvite(i)
	return &invite, nil
}

func (r *VaultInviteRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.VaultInvite, error) {
	i, err := r.q(ctx).GetInviteByTokenHash(ctx, &tokenHash)
	if err != nil {
		return nil, mapError(err)
	}
	invite := toInvite(i)
	return &invite, nil
}

func (r *VaultInviteRepository) RotateToken(ctx context.Context, id uuid.UUID, tokenHash string) error {
	return expectRows(r.q(ctx).RotateInviteToken(ctx, db.RotateInviteTokenParams{ID: id, TokenHash: &tokenHash}))
}

func (r *VaultInviteRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error) {
	rows, err := r.q(ctx).ListInvitesByVault(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	invites := make([]domain.VaultInvite, len(rows))
	for i, inv := range rows {
		invites[i] = toInvite(inv)
	}
	return invites, nil
}

func (r *VaultInviteRepository) ListPendingByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error) {
	rows, err := r.q(ctx).ListPendingInvitesByVault(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	invites := make([]domain.VaultInvite, len(rows))
	for i, inv := range rows {
		invites[i] = toInvite(inv)
	}
	return invites, nil
}

func (r *VaultInviteRepository) PendingExists(ctx context.Context, vaultID uuid.UUID, email string) (bool, error) {
	exists, err := r.q(ctx).PendingInviteExists(ctx, db.PendingInviteExistsParams{VaultID: vaultID, Email: email})
	return exists, mapError(err)
}

func (r *VaultInviteRepository) ListByEmail(ctx context.Context, email string) ([]domain.VaultInvite, error) {
	details, err := r.ListPendingForEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	invites := make([]domain.VaultInvite, len(details))
	for i, d := range details {
		invites[i] = d.VaultInvite
	}
	return invites, nil
}

func (r *VaultInviteRepository) ListPendingForEmail(ctx context.Context, email string) ([]domain.InviteDetails, error) {
	rows, err := r.q(ctx).ListPendingInvitesForEmail(ctx, strings.ToLower(email))
	if err != nil {
		return nil, mapError(err)
	}
	details := make([]domain.InviteDetails, len(rows))
	for i, row := range rows {
		details[i] = domain.InviteDetails{
			VaultInvite: toInvite(db.VaultInvite{
				ID: row.ID, VaultID: row.VaultID, InviterID: row.InviterID, InviteeEmail: row.InviteeEmail,
				Role: row.Role, EncryptedVaultKey: row.EncryptedVaultKey, Accepted: row.Accepted,
				ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, TokenHash: row.TokenHash, Grants: row.Grants,
			}),
			VaultName:    row.VaultName,
			InviterName:  row.InviterName,
			InviterEmail: row.InviterEmail,
		}
	}
	return details, nil
}

func (r *VaultInviteRepository) Accept(ctx context.Context, id uuid.UUID) error {
	n, err := r.q(ctx).AcceptInvite(ctx, id)
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrAlreadyExists
	}
	return nil
}

func (r *VaultInviteRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return expectRows(r.q(ctx).DeleteInvite(ctx, id))
}

func (r *VaultInviteRepository) DeleteExpired(ctx context.Context) (int64, error) {
	n, err := r.q(ctx).DeleteExpiredInvites(ctx)
	return n, mapError(err)
}

type VaultKeyRepository struct{ store }

func NewVaultKeyRepository(pool *pgxpool.Pool) *VaultKeyRepository {
	return &VaultKeyRepository{store{pool}}
}

func toVaultKey(k db.VaultKey) domain.VaultKey {
	return domain.VaultKey{VaultID: k.VaultID, UserID: k.UserID, EncryptedKey: k.EncryptedKey, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt}
}

func (r *VaultKeyRepository) Upsert(ctx context.Context, k *domain.VaultKey) error {
	return mapError(r.q(ctx).UpsertVaultKey(ctx, db.UpsertVaultKeyParams{
		VaultID: k.VaultID, UserID: k.UserID, EncryptedKey: k.EncryptedKey, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt,
	}))
}

func (r *VaultKeyRepository) GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultKey, error) {
	k, err := r.q(ctx).GetVaultKey(ctx, db.GetVaultKeyParams{VaultID: vaultID, UserID: userID})
	if err != nil {
		return nil, mapError(err)
	}
	key := toVaultKey(k)
	return &key, nil
}

func (r *VaultKeyRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultKey, error) {
	rows, err := r.q(ctx).ListVaultKeys(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	keys := make([]domain.VaultKey, len(rows))
	for i, k := range rows {
		keys[i] = toVaultKey(k)
	}
	return keys, nil
}

func (r *VaultKeyRepository) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	return mapError(r.q(ctx).DeleteVaultKeysForUser(ctx, userID))
}

func (r *VaultKeyRepository) HasAny(ctx context.Context, vaultID uuid.UUID) (bool, error) {
	exists, err := r.q(ctx).VaultHasKeys(ctx, vaultID)
	return exists, mapError(err)
}

func (r *VaultKeyRepository) Delete(ctx context.Context, vaultID, userID uuid.UUID) error {
	return mapError(r.q(ctx).DeleteVaultKey(ctx, db.DeleteVaultKeyParams{VaultID: vaultID, UserID: userID}))
}

type VaultEncryptionRepository struct{ store }

func NewVaultEncryptionRepository(pool *pgxpool.Pool) *VaultEncryptionRepository {
	return &VaultEncryptionRepository{store{pool}}
}

func (r *VaultEncryptionRepository) Upsert(ctx context.Context, e *domain.VaultEncryption) error {
	return mapError(r.q(ctx).UpsertVaultEncryption(ctx, db.UpsertVaultEncryptionParams{
		VaultID: e.VaultID, Salt: e.Salt, EncryptedVek: e.EncryptedVEK, CreatedAt: e.CreatedAt,
	}))
}

func (r *VaultEncryptionRepository) GetByVaultID(ctx context.Context, vaultID uuid.UUID) (*domain.VaultEncryption, error) {
	e, err := r.q(ctx).GetVaultEncryption(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	return &domain.VaultEncryption{VaultID: e.VaultID, Salt: e.Salt, EncryptedVEK: e.EncryptedVek, CreatedAt: e.CreatedAt}, nil
}
