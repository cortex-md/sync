package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type UploadSnapshotInput struct {
	UserID      uuid.UUID
	DeviceID    uuid.UUID
	VaultID     uuid.UUID
	FilePath    string
	Checksum    string
	ContentType string
	SizeBytes   int64
	BaseVersion *int
	Data        io.Reader
}

func (uc *Service) UploadSnapshot(ctx context.Context, input UploadSnapshotInput) (*FileInfo, error) {
	if input.FilePath == "" {
		return nil, domain.ErrInvalidInput
	}

	if _, err := uc.authz.RequirePath(ctx, input.VaultID, input.UserID, input.FilePath, domain.PermFileWrite); err != nil {
		return nil, err
	}

	if uc.maxFileSize > 0 && input.SizeBytes > uc.maxFileSize {
		return nil, domain.ErrFileTooLarge
	}

	existing, err := uc.latest.Get(ctx, input.VaultID, input.FilePath)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		if isSameContent(existing, input.Checksum) {
			return fileInfoFromLatest(existing), nil
		}
		if input.BaseVersion != nil && *input.BaseVersion != existing.CurrentVersion {
			return nil, domain.ErrConflict
		}
	}
	if err := uc.checkQuota(ctx, input.VaultID, input.SizeBytes); err != nil {
		return nil, err
	}

	contentType := input.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	snapshotID := uuid.New()
	blobKey := blobKeyForSnapshot(input.VaultID, snapshotID)
	if uc.blobQueue != nil {
		if err := uc.blobQueue.RegisterPending(ctx, blobKey); err != nil {
			return nil, err
		}
	}
	sizeBytes, err := uc.blobs.Upload(ctx, blobKey, input.Data, input.SizeBytes, contentType)
	if err != nil {
		uc.discardUpload(ctx, blobKey)
		return nil, err
	}
	if uc.maxFileSize > 0 && sizeBytes > uc.maxFileSize {
		uc.discardUpload(ctx, blobKey)
		return nil, domain.ErrFileTooLarge
	}

	now := time.Now()
	var (
		fl        *domain.FileLatest
		duplicate *domain.FileLatest
		syncEvent *domain.SyncEvent
	)

	err = uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		current, txErr := uc.latest.GetForUpdate(ctx, input.VaultID, input.FilePath)
		if txErr != nil && !errors.Is(txErr, domain.ErrNotFound) {
			return txErr
		}

		version := 1
		createdAt := now
		createdBy := input.UserID
		if current != nil {
			if isSameContent(current, input.Checksum) {
				duplicate = current
				return nil
			}
			if input.BaseVersion != nil && *input.BaseVersion != current.CurrentVersion {
				return domain.ErrConflict
			}
			version = current.CurrentVersion + 1
			createdAt = current.CreatedAt
			if current.CreatedBy != uuid.Nil {
				createdBy = current.CreatedBy
			}
		}

		if txErr = uc.snapshots.Create(ctx, &domain.FileSnapshot{
			ID:               snapshotID,
			VaultID:          input.VaultID,
			FilePath:         input.FilePath,
			Version:          version,
			EncryptedBlobKey: blobKey,
			SizeBytes:        sizeBytes,
			Checksum:         input.Checksum,
			CreatedBy:        input.UserID,
			DeviceID:         input.DeviceID,
			CreatedAt:        now,
		}); txErr != nil {
			if errors.Is(txErr, domain.ErrAlreadyExists) {
				return domain.ErrConflict
			}
			return txErr
		}

		fl = &domain.FileLatest{
			VaultID:               input.VaultID,
			FilePath:              input.FilePath,
			CurrentVersion:        version,
			LatestSnapshotVersion: version,
			Checksum:              input.Checksum,
			SizeBytes:             sizeBytes,
			ContentType:           contentType,
			LastModifiedBy:        input.UserID,
			LastDeviceID:          input.DeviceID,
			CreatedBy:             createdBy,
			UpdatedAt:             now,
			CreatedAt:             createdAt,
		}
		if txErr = uc.latest.Upsert(ctx, fl); txErr != nil {
			return txErr
		}
		if txErr = uc.resolveUpload(ctx, blobKey); txErr != nil {
			return txErr
		}

		freed, txErr := uc.pruneOldSnapshots(ctx, input.VaultID, input.FilePath)
		if txErr != nil {
			return txErr
		}
		freedDeltas, txErr := uc.deltas.DeleteByFilePath(ctx, input.VaultID, input.FilePath, version)
		if txErr != nil {
			return txErr
		}
		if txErr = uc.addStorage(ctx, input.VaultID, sizeBytes-freed-freedDeltas); txErr != nil {
			return txErr
		}

		eventType := domain.EventFileUpdated
		if current == nil || current.Deleted {
			eventType = domain.EventFileCreated
		}
		syncEvent = &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: eventType,
			FilePath:  input.FilePath,
			Version:   version,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{"checksum": input.Checksum, "size_bytes": sizeBytes},
			CreatedAt: now,
		}
		return uc.events.Create(ctx, syncEvent)
	})
	if (err != nil && !errors.Is(err, domain.ErrCommitUncertain)) || duplicate != nil {
		uc.discardUpload(ctx, blobKey)
	}
	if err != nil {
		return nil, err
	}
	if duplicate != nil {
		info := fileInfoFromLatest(duplicate)
		info.Unchanged = true
		return info, nil
	}

	uc.publishSSE(syncEvent)

	info := fileInfoFromLatest(fl)
	info.SnapshotID = snapshotID
	return info, nil
}

func isSameContent(latest *domain.FileLatest, checksum string) bool {
	return checksum != "" && !latest.Deleted && latest.Checksum == checksum
}

type UploadDeltaInput struct {
	UserID        uuid.UUID
	DeviceID      uuid.UUID
	VaultID       uuid.UUID
	FilePath      string
	BaseVersion   int
	Checksum      string
	SizeBytes     int64
	EncryptedData []byte
}

func (uc *Service) UploadDelta(ctx context.Context, input UploadDeltaInput) (*FileInfo, error) {
	if input.FilePath == "" || len(input.EncryptedData) == 0 {
		return nil, domain.ErrInvalidInput
	}

	if _, err := uc.authz.RequirePath(ctx, input.VaultID, input.UserID, input.FilePath, domain.PermFileWrite); err != nil {
		return nil, err
	}

	deltaSize := int64(len(input.EncryptedData))
	if uc.maxDeltaBytes > 0 && deltaSize > uc.maxDeltaBytes {
		return nil, domain.ErrDeltaTooLarge
	}
	if uc.maxFileSize > 0 && deltaSize > uc.maxFileSize {
		return nil, domain.ErrFileTooLarge
	}

	now := time.Now()
	var (
		existing  *domain.FileLatest
		fl        *domain.FileLatest
		syncEvent *domain.SyncEvent
		replayed  bool
	)

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		var txErr error
		existing, txErr = uc.latest.GetForUpdate(ctx, input.VaultID, input.FilePath)
		if txErr != nil {
			return txErr
		}

		if isDeltaReplay(existing, input) {
			fl, replayed = existing, true
			return nil
		}
		if existing.Deleted || existing.CurrentVersion != input.BaseVersion {
			return domain.ErrConflict
		}

		targetVersion := existing.CurrentVersion + 1
		if txErr = uc.deltas.Create(ctx, &domain.FileDelta{
			ID:             uuid.New(),
			VaultID:        input.VaultID,
			FilePath:       input.FilePath,
			BaseVersion:    input.BaseVersion,
			TargetVersion:  targetVersion,
			EncryptedDelta: input.EncryptedData,
			SizeBytes:      deltaSize,
			CreatedBy:      input.UserID,
			DeviceID:       input.DeviceID,
			CreatedAt:      now,
		}); txErr != nil {
			if errors.Is(txErr, domain.ErrAlreadyExists) {
				return domain.ErrConflict
			}
			return txErr
		}

		updated := *existing
		updated.CurrentVersion = targetVersion
		updated.Checksum = input.Checksum
		updated.SizeBytes = input.SizeBytes
		updated.LastModifiedBy = input.UserID
		updated.LastDeviceID = input.DeviceID
		updated.DeltasSinceSnapshot = existing.DeltasSinceSnapshot + 1
		updated.DeltaBytesSinceSnapshot = existing.DeltaBytesSinceSnapshot + deltaSize
		updated.UpdatedAt = now
		fl = &updated
		if txErr = uc.latest.Upsert(ctx, fl); txErr != nil {
			return txErr
		}
		if txErr = uc.addStorage(ctx, input.VaultID, deltaSize); txErr != nil {
			return txErr
		}

		syncEvent = &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: domain.EventFileUpdated,
			FilePath:  input.FilePath,
			Version:   targetVersion,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{"checksum": input.Checksum, "delta": true, "base_version": input.BaseVersion},
			CreatedAt: now,
		}
		return uc.events.Create(ctx, syncEvent)
	}); err != nil {
		return nil, err
	}
	if replayed {
		return fileInfoFromLatest(fl), nil
	}
	uc.publishSSE(syncEvent)

	info := fileInfoFromLatest(fl)
	info.NeedsSnapshot = uc.needsSnapshot(fl, existing.SizeBytes)
	return info, nil
}

func isDeltaReplay(existing *domain.FileLatest, input UploadDeltaInput) bool {
	return !existing.Deleted &&
		existing.CurrentVersion == input.BaseVersion+1 &&
		input.Checksum != "" &&
		existing.Checksum == input.Checksum &&
		existing.LastDeviceID == input.DeviceID
}

func (uc *Service) needsSnapshot(fl *domain.FileLatest, baseSize int64) bool {
	policy := uc.deltaPolicy
	if policy.MaxDeltasBeforeSnapshot > 0 && fl.DeltasSinceSnapshot >= policy.MaxDeltasBeforeSnapshot {
		return true
	}
	if policy.MaxDeltaSizeRatio > 0 && baseSize > 0 {
		return float64(fl.DeltaBytesSinceSnapshot)/float64(baseSize) >= policy.MaxDeltaSizeRatio
	}
	return false
}

func blobKeyForSnapshot(vaultID uuid.UUID, snapshotID uuid.UUID) string {
	return fmt.Sprintf("vaults/%s/blobs/%s", vaultID, snapshotID)
}

func (uc *Service) UploadSnapshotFromBytes(ctx context.Context, input UploadSnapshotInput, data []byte) (*FileInfo, error) {
	input.Data = bytes.NewReader(data)
	input.SizeBytes = int64(len(data))
	return uc.UploadSnapshot(ctx, input)
}
