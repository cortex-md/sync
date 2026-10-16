package file

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type DeleteFileInput struct {
	UserID      uuid.UUID
	DeviceID    uuid.UUID
	VaultID     uuid.UUID
	FilePath    string
	BaseVersion *int
}

func (uc *Service) DeleteFile(ctx context.Context, input DeleteFileInput) error {
	if input.FilePath == "" {
		return domain.ErrInvalidInput
	}
	if _, err := uc.authz.RequirePath(ctx, input.VaultID, input.UserID, input.FilePath, domain.PermFileWrite); err != nil {
		return err
	}

	now := time.Now()
	var syncEvent *domain.SyncEvent
	accessChanged := false

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		existing, txErr := uc.latest.GetForUpdate(ctx, input.VaultID, input.FilePath)
		if txErr != nil {
			return txErr
		}
		if existing.Deleted {
			return domain.ErrNotFound
		}
		if input.BaseVersion != nil && *input.BaseVersion != existing.CurrentVersion {
			return domain.ErrConflict
		}

		existing.Deleted = true
		existing.LastModifiedBy = input.UserID
		existing.LastDeviceID = input.DeviceID
		existing.UpdatedAt = now
		existing.CurrentVersion++
		if txErr = uc.latest.Upsert(ctx, existing); txErr != nil {
			return txErr
		}
		if uc.shares != nil {
			if txErr = uc.shares.RemoveForPath(ctx, input.VaultID, input.FilePath); txErr != nil {
				return txErr
			}
		}
		if uc.collabDocs != nil {
			if txErr = uc.collabDocs.DeleteForPath(ctx, input.VaultID, input.FilePath); txErr != nil {
				return txErr
			}
		}
		if uc.access != nil {
			if accessChanged, txErr = uc.access.RemoveForPath(ctx, input.VaultID, input.FilePath); txErr != nil {
				return txErr
			}
		}

		syncEvent = &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: domain.EventFileDeleted,
			FilePath:  input.FilePath,
			Version:   existing.CurrentVersion,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{},
			CreatedAt: now,
		}
		return uc.events.Create(ctx, syncEvent)
	}); err != nil {
		return err
	}
	uc.publishSSE(syncEvent)
	if accessChanged {
		uc.access.Changed(ctx, input.VaultID)
	}
	return nil
}

type RestoreFileInput struct {
	UserID   uuid.UUID
	DeviceID uuid.UUID
	VaultID  uuid.UUID
	FilePath string
}

func (uc *Service) RestoreFile(ctx context.Context, input RestoreFileInput) error {
	if input.FilePath == "" {
		return domain.ErrInvalidInput
	}
	if _, err := uc.authz.RequirePath(ctx, input.VaultID, input.UserID, input.FilePath, domain.PermFileWrite); err != nil {
		return err
	}

	now := time.Now()
	var syncEvent *domain.SyncEvent

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		existing, txErr := uc.latest.GetForUpdate(ctx, input.VaultID, input.FilePath)
		if txErr != nil {
			return txErr
		}
		if !existing.Deleted {
			return domain.ErrInvalidInput
		}
		if _, txErr = uc.snapshots.GetLatest(ctx, input.VaultID, input.FilePath); txErr != nil {
			return txErr
		}

		existing.Deleted = false
		existing.LastModifiedBy = input.UserID
		existing.LastDeviceID = input.DeviceID
		existing.UpdatedAt = now
		existing.CurrentVersion++
		if txErr = uc.latest.Upsert(ctx, existing); txErr != nil {
			return txErr
		}

		syncEvent = &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: domain.EventFileCreated,
			FilePath:  input.FilePath,
			Version:   existing.CurrentVersion,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{},
			CreatedAt: now,
		}
		return uc.events.Create(ctx, syncEvent)
	}); err != nil {
		return err
	}
	uc.publishSSE(syncEvent)
	return nil
}

type RenameFileInput struct {
	UserID   uuid.UUID
	DeviceID uuid.UUID
	VaultID  uuid.UUID
	OldPath  string
	NewPath  string
}

func (uc *Service) RenameFile(ctx context.Context, input RenameFileInput) (*FileInfo, error) {
	if input.OldPath == "" || input.NewPath == "" || input.OldPath == input.NewPath {
		return nil, domain.ErrInvalidInput
	}
	if _, err := uc.authz.RequirePath(ctx, input.VaultID, input.UserID, input.OldPath, domain.PermFileWrite); err != nil {
		return nil, err
	}
	if _, err := uc.authz.RequirePath(ctx, input.VaultID, input.UserID, input.NewPath, domain.PermFileWrite); err != nil {
		return nil, err
	}

	now := time.Now()
	var result *FileInfo
	var syncEvent *domain.SyncEvent
	accessChanged := false

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		locked, txErr := uc.lockPaths(ctx, input.VaultID, input.OldPath, input.NewPath)
		if txErr != nil {
			return txErr
		}
		existing, target := locked[input.OldPath], locked[input.NewPath]
		if existing == nil || existing.Deleted {
			return domain.ErrNotFound
		}
		if target != nil {
			if !target.Deleted {
				return domain.ErrAlreadyExists
			}
			if txErr = uc.purgePath(ctx, input.VaultID, input.NewPath); txErr != nil {
				return txErr
			}
		}
		if uc.shares != nil {
			if txErr = uc.shares.RemoveForPath(ctx, input.VaultID, input.NewPath); txErr != nil {
				return txErr
			}
			if txErr = uc.shares.MovePath(ctx, input.VaultID, input.OldPath, input.NewPath); txErr != nil {
				return txErr
			}
		}

		if txErr = uc.snapshots.MovePath(ctx, input.VaultID, input.OldPath, input.NewPath); txErr != nil {
			return txErr
		}
		if txErr = uc.deltas.MovePath(ctx, input.VaultID, input.OldPath, input.NewPath); txErr != nil {
			return txErr
		}
		if uc.collabDocs != nil {
			if txErr = uc.collabDocs.MovePath(ctx, input.VaultID, input.OldPath, input.NewPath); txErr != nil {
				return txErr
			}
		}
		if uc.access != nil {
			if accessChanged, txErr = uc.access.MovePath(ctx, input.VaultID, input.OldPath, input.NewPath); txErr != nil {
				return txErr
			}
		}

		currentVersion := existing.CurrentVersion
		if target != nil && target.CurrentVersion > currentVersion {
			currentVersion = target.CurrentVersion
		}

		moved := *existing
		moved.FilePath = input.NewPath
		moved.CurrentVersion = currentVersion + 1
		moved.Deleted = false
		moved.LastModifiedBy = input.UserID
		moved.LastDeviceID = input.DeviceID
		moved.UpdatedAt = now
		if txErr = uc.latest.Upsert(ctx, &moved); txErr != nil {
			return txErr
		}

		existing.Deleted = true
		existing.LastModifiedBy = input.UserID
		existing.LastDeviceID = input.DeviceID
		existing.UpdatedAt = now
		existing.CurrentVersion++
		existing.DeltasSinceSnapshot = 0
		existing.DeltaBytesSinceSnapshot = 0
		if txErr = uc.latest.Upsert(ctx, existing); txErr != nil {
			return txErr
		}

		syncEvent = &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: domain.EventFileRenamed,
			FilePath:  input.NewPath,
			Version:   moved.CurrentVersion,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{"old_path": input.OldPath, "new_path": input.NewPath},
			CreatedAt: now,
		}
		if txErr = uc.events.Create(ctx, syncEvent); txErr != nil {
			return txErr
		}
		result = fileInfoFromLatest(&moved)
		return nil
	}); err != nil {
		return nil, err
	}
	uc.publishSSE(syncEvent)
	if accessChanged {
		uc.access.Changed(ctx, input.VaultID)
	}
	return result, nil
}

func (uc *Service) purgePath(ctx context.Context, vaultID uuid.UUID, path string) error {
	purged, err := uc.snapshots.DeleteByFilePath(ctx, vaultID, path)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(purged))
	var freed int64
	for _, snapshot := range purged {
		keys = append(keys, snapshot.EncryptedBlobKey)
		freed += snapshot.SizeBytes
	}
	if err := uc.releaseBlobs(ctx, keys); err != nil {
		return err
	}
	freedDeltas, err := uc.deltas.DeleteByFilePath(ctx, vaultID, path, math.MaxInt32)
	if err != nil {
		return err
	}
	return uc.addStorage(ctx, vaultID, -freed-freedDeltas)
}

func (uc *Service) lockPaths(ctx context.Context, vaultID uuid.UUID, paths ...string) (map[string]*domain.FileLatest, error) {
	ordered := slices.Clone(paths)
	slices.Sort(ordered)
	locked := make(map[string]*domain.FileLatest, len(ordered))
	for _, path := range slices.Compact(ordered) {
		fl, err := uc.latest.GetForUpdate(ctx, vaultID, path)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		locked[path] = fl
	}
	return locked, nil
}
