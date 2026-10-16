package share

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	wrappedKeyLength = 12 + 32 + 16
	expiredBatchSize = 100
)

type Service struct {
	maxPerVault  int
	shares       ShareRepository
	vaults       VaultStorage
	files        FileLookup
	blobs        BlobStorage
	queue        BlobQueue
	tx           Transactor
	authz        *authz.Authorizer
	maxBytes     int64
	enforceQuota bool
	entitled     func(ctx context.Context, vaultID uuid.UUID) error
}

type Deps struct {
	Shares       ShareRepository
	Vaults       VaultStorage
	Files        FileLookup
	Blobs        BlobStorage
	BlobQueue    BlobQueue
	Tx           Transactor
	Authz        *authz.Authorizer
	MaxBytes     int64
	MaxPerVault  int
	EnforceQuota bool
	Entitled     func(ctx context.Context, vaultID uuid.UUID) error
}

func NewService(d Deps) *Service {
	return &Service{
		shares:       d.Shares,
		vaults:       d.Vaults,
		files:        d.Files,
		blobs:        d.Blobs,
		queue:        d.BlobQueue,
		maxPerVault:  d.MaxPerVault,
		tx:           d.Tx,
		authz:        d.Authz,
		maxBytes:     d.MaxBytes,
		enforceQuota: d.EnforceQuota,
		entitled:     d.Entitled,
	}
}

func newShareID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Service) List(ctx context.Context, userID, vaultID uuid.UUID) ([]domain.NoteShare, error) {
	access, err := s.authz.Access(ctx, vaultID, userID, domain.PermFileRead)
	if err != nil {
		return nil, err
	}
	shares, err := s.shares.ListByVault(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	visible := shares[:0]
	for _, share := range shares {
		if access.Scope.CanRead(share.FilePath) {
			visible = append(visible, share)
		}
	}
	return visible, nil
}

type CreateInput struct {
	UserID     uuid.UUID
	VaultID    uuid.UUID
	FilePath   string
	WrappedKey []byte
	ExpiresAt  *time.Time
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*domain.NoteShare, bool, error) {
	if input.FilePath == "" || len(input.WrappedKey) != wrappedKeyLength {
		return nil, false, domain.ErrInvalidInput
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return nil, false, domain.ErrInvalidInput
	}
	if err := s.requireShareable(ctx, input.VaultID, input.UserID, input.FilePath); err != nil {
		return nil, false, err
	}
	latest, err := s.files.Get(ctx, input.VaultID, input.FilePath)
	if err != nil {
		return nil, false, err
	}
	if latest.Deleted {
		return nil, false, domain.ErrNotFound
	}
	if err := s.checkShareLimit(ctx, input.VaultID, input.FilePath); err != nil {
		return nil, false, err
	}

	id, err := newShareID()
	if err != nil {
		return nil, false, err
	}
	share := &domain.NoteShare{
		ID:         id,
		VaultID:    input.VaultID,
		FilePath:   input.FilePath,
		CreatedBy:  input.UserID,
		WrappedKey: input.WrappedKey,
		ExpiresAt:  input.ExpiresAt,
	}
	created, err := s.shares.Create(ctx, share)
	if err != nil {
		return nil, false, err
	}
	if !created {
		existing, err := s.shares.GetByPath(ctx, input.VaultID, input.FilePath)
		return existing, false, err
	}
	return share, true, nil
}

type PublishInput struct {
	UserID  uuid.UUID
	VaultID uuid.UUID
	ShareID string
	Size    int64
	Body    io.Reader
}

func (s *Service) Publish(ctx context.Context, input PublishInput) (*domain.NoteShare, error) {
	share, err := s.ownedShare(ctx, input.VaultID, input.ShareID)
	if err != nil {
		return nil, err
	}
	if err := s.requireShareable(ctx, share.VaultID, input.UserID, share.FilePath); err != nil {
		return nil, err
	}
	if input.Size <= 0 {
		return nil, domain.ErrInvalidInput
	}
	if s.maxBytes > 0 && input.Size > s.maxBytes {
		return nil, domain.ErrFileTooLarge
	}

	blobKey := fmt.Sprintf("shares/%s/%s", share.ID, uuid.NewString())
	if err := s.queue.RegisterPending(ctx, blobKey); err != nil {
		return nil, err
	}
	size, err := s.blobs.Upload(ctx, blobKey, io.LimitReader(input.Body, input.Size), input.Size, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if size != input.Size {
		return nil, domain.ErrInvalidInput
	}

	var published *domain.NoteShare
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		current, err := s.shares.GetForUpdate(ctx, share.ID)
		if err != nil {
			return err
		}
		if err := s.adjustStorage(ctx, current.VaultID, size-current.SizeBytes); err != nil {
			return err
		}
		if err := s.shares.UpdateContent(ctx, current.ID, current.Revision+1, blobKey, size); err != nil {
			return err
		}
		if err := s.queue.ResolvePending(ctx, blobKey); err != nil {
			return err
		}
		if current.BlobKey != "" {
			if err := s.queue.EnqueueDeletion(ctx, current.BlobKey); err != nil {
				return err
			}
		}
		current.Revision++
		current.BlobKey = blobKey
		current.SizeBytes = size
		published = current
		return nil
	})
	if err != nil {
		if enqueueErr := s.queue.EnqueueDeletion(context.WithoutCancel(ctx), blobKey); enqueueErr != nil {
			log.Warn().Err(enqueueErr).Str("blob_key", blobKey).Msg("share: failed to release rejected upload")
		}
		return nil, err
	}
	return published, nil
}

func (s *Service) UpdateExpiry(ctx context.Context, userID, vaultID uuid.UUID, shareID string, expiresAt *time.Time) (*domain.NoteShare, error) {
	share, err := s.ownedShare(ctx, vaultID, shareID)
	if err != nil {
		return nil, err
	}
	if _, err := s.authz.RequirePath(ctx, share.VaultID, userID, share.FilePath, domain.PermNoteShare); err != nil {
		return nil, err
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, domain.ErrInvalidInput
	}
	if err := s.shares.UpdateExpiry(ctx, share.ID, expiresAt); err != nil {
		return nil, err
	}
	share.ExpiresAt = expiresAt
	return share, nil
}

func (s *Service) Revoke(ctx context.Context, userID, vaultID uuid.UUID, shareID string) error {
	share, err := s.ownedShare(ctx, vaultID, shareID)
	if err != nil {
		return err
	}
	if _, err := s.authz.RequirePath(ctx, share.VaultID, userID, share.FilePath, domain.PermNoteShare); err != nil {
		return err
	}
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		removed, err := s.shares.Delete(ctx, share.ID)
		if err != nil {
			return err
		}
		return s.release(ctx, []domain.RemovedShare{*removed})
	})
}

func (s *Service) RemoveForPath(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	removed, err := s.shares.DeleteByPath(ctx, vaultID, filePath)
	if err != nil {
		return err
	}
	return s.release(ctx, removed)
}

func (s *Service) MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	return s.shares.Move(ctx, vaultID, oldPath, newPath)
}

func (s *Service) RemoveExpired(ctx context.Context) (int64, error) {
	var count int64
	err := s.tx.RunInTx(ctx, func(ctx context.Context) error {
		removed, err := s.shares.DeleteExpired(ctx, expiredBatchSize)
		if err != nil {
			return err
		}
		count = int64(len(removed))
		return s.release(ctx, removed)
	})
	return count, err
}

func (s *Service) Resolve(ctx context.Context, shareID string) (*domain.NoteShare, error) {
	share, err := s.shares.Get(ctx, shareID)
	if err != nil {
		return nil, err
	}
	if !share.Available(time.Now()) {
		return nil, domain.ErrNotFound
	}
	vault, err := s.vaults.GetByID(ctx, share.VaultID)
	if err != nil {
		return nil, err
	}
	if !vault.AllowPublicLinks {
		return nil, domain.ErrNotFound
	}
	if s.entitled != nil {
		if err := s.entitled(ctx, share.VaultID); err != nil {
			if errors.Is(err, domain.ErrSubscriptionRequired) || errors.Is(err, domain.ErrSubscriptionExpired) {
				return nil, domain.ErrNotFound
			}
			return nil, err
		}
	}
	return share, nil
}

func (s *Service) Open(ctx context.Context, share *domain.NoteShare) (io.ReadCloser, error) {
	return s.blobs.Download(ctx, share.BlobKey)
}

func (s *Service) ownedShare(ctx context.Context, vaultID uuid.UUID, shareID string) (*domain.NoteShare, error) {
	share, err := s.shares.Get(ctx, shareID)
	if err != nil {
		return nil, err
	}
	if share.VaultID != vaultID {
		return nil, domain.ErrNotFound
	}
	return share, nil
}

func (s *Service) requireShareable(ctx context.Context, vaultID, userID uuid.UUID, filePath string) error {
	if _, err := s.authz.RequirePath(ctx, vaultID, userID, filePath, domain.PermNoteShare); err != nil {
		return err
	}
	vault, err := s.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return err
	}
	if !vault.AllowPublicLinks {
		return domain.ErrPublicLinksDisabled
	}
	return nil
}

func (s *Service) adjustStorage(ctx context.Context, vaultID uuid.UUID, delta int64) error {
	if delta == 0 {
		return nil
	}
	if s.enforceQuota && delta > 0 {
		return s.vaults.AddStorageWithinQuota(ctx, vaultID, delta)
	}
	return s.vaults.AddStorage(ctx, vaultID, delta)
}

func (s *Service) release(ctx context.Context, removed []domain.RemovedShare) error {
	freed := make(map[uuid.UUID]int64)
	keys := make([]string, 0, len(removed))
	for _, share := range removed {
		freed[share.VaultID] += share.SizeBytes
		if share.BlobKey != "" {
			keys = append(keys, share.BlobKey)
		}
	}
	if len(keys) > 0 {
		if err := s.queue.EnqueueDeletion(ctx, keys...); err != nil {
			return err
		}
	}
	for vaultID, size := range freed {
		if err := s.vaults.AddStorage(ctx, vaultID, -size); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Service) checkShareLimit(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	if s.maxPerVault <= 0 {
		return nil
	}
	existing, err := s.shares.ListByVault(ctx, vaultID)
	if err != nil {
		return err
	}
	for i := range existing {
		if existing[i].FilePath == filePath {
			return nil
		}
	}
	if len(existing) >= s.maxPerVault {
		return domain.ErrLimitReached
	}
	return nil
}
