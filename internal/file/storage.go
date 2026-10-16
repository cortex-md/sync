package file

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

func (uc *Service) checkQuota(ctx context.Context, vaultID uuid.UUID, incoming int64) error {
	if !uc.enforceQuota || uc.vaults == nil {
		return nil
	}
	vault, err := uc.vaults.GetByID(ctx, vaultID)
	if err != nil {
		return err
	}
	if vault.StorageQuotaBytes != nil && vault.StorageUsedBytes+max(incoming, 0) > *vault.StorageQuotaBytes {
		return domain.ErrQuotaExceeded
	}
	return nil
}

func (uc *Service) addStorage(ctx context.Context, vaultID uuid.UUID, delta int64) error {
	if uc.vaults == nil || delta == 0 {
		return nil
	}
	if uc.enforceQuota && delta > 0 {
		return uc.vaults.AddStorageWithinQuota(ctx, vaultID, delta)
	}
	return uc.vaults.AddStorage(ctx, vaultID, delta)
}

func (uc *Service) resolveUpload(ctx context.Context, key string) error {
	if uc.blobQueue == nil {
		return nil
	}
	return uc.blobQueue.ResolvePending(ctx, key)
}

func (uc *Service) discardUpload(ctx context.Context, key string) {
	ctx = context.WithoutCancel(ctx)
	if err := uc.blobs.Delete(ctx, key); err != nil {
		return
	}
	if uc.blobQueue != nil {
		_ = uc.blobQueue.ResolvePending(ctx, key)
	}
}

func (uc *Service) releaseBlobs(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if uc.blobQueue != nil {
		return uc.blobQueue.EnqueueDeletion(ctx, keys...)
	}
	cleanup := context.WithoutCancel(ctx)
	for _, key := range keys {
		_ = uc.blobs.Delete(cleanup, key)
	}
	return nil
}

func (uc *Service) pruneOldSnapshots(ctx context.Context, vaultID uuid.UUID, filePath string) (int64, error) {
	if uc.maxSnapshotsPerFile <= 0 {
		return 0, nil
	}

	deleted, err := uc.snapshots.DeleteOlderVersions(ctx, vaultID, filePath, uc.maxSnapshotsPerFile)
	if err != nil {
		return 0, err
	}

	keys := make([]string, 0, len(deleted))
	var freed int64
	for _, s := range deleted {
		keys = append(keys, s.EncryptedBlobKey)
		freed += s.SizeBytes
	}
	return freed, uc.releaseBlobs(ctx, keys)
}
