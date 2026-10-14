package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/google/uuid"
)

const (
	BatchMaxItems           = 200
	BatchMaxBytes     int64 = 64 << 20
	maxListPageSize         = 5000
	maxHistoryEntries       = 200
)

type Service struct {
	snapshots           FileSnapshotRepository
	deltas              FileDeltaRepository
	latest              FileLatestRepository
	events              SyncEventRepository
	devices             DeviceRepository
	vaults              VaultStorage
	blobs               BlobStorage
	blobQueue           BlobQueue
	publisher           EventPublisher
	tx                  Transactor
	authz               *authz.Authorizer
	collabDocs          CollabDocumentRepository
	shares              SharePaths
	access              AccessPaths
	deltaPolicy         DeltaPolicy
	maxFileSize         int64
	maxSnapshotsPerFile int
	enforceQuota        bool
}

type DeltaPolicy struct {
	MaxDeltasBeforeSnapshot int
	MaxDeltaSizeRatio       float64
}

func DefaultDeltaPolicy() DeltaPolicy {
	return DeltaPolicy{
		MaxDeltasBeforeSnapshot: 10,
		MaxDeltaSizeRatio:       0.5,
	}
}

type EventPublisher interface {
	PublishSyncEvent(event *domain.SyncEvent)
}

type Deps struct {
	Snapshots           FileSnapshotRepository
	Deltas              FileDeltaRepository
	Latest              FileLatestRepository
	Events              SyncEventRepository
	Members             VaultMemberRepository
	Users               UserRepository
	Devices             DeviceRepository
	Vaults              VaultStorage
	Blobs               BlobStorage
	BlobQueue           BlobQueue
	Tx                  Transactor
	Authz               *authz.Authorizer
	Publisher           EventPublisher
	CollabDocs          CollabDocumentRepository
	Shares              SharePaths
	Access              AccessPaths
	DeltaPolicy         *DeltaPolicy
	MaxFileSize         int64
	MaxSnapshotsPerFile int
	EnforceQuota        bool
}

func NewService(d Deps) *Service {
	policy := DefaultDeltaPolicy()
	if d.DeltaPolicy != nil {
		policy = *d.DeltaPolicy
	}
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &Service{
		snapshots:           d.Snapshots,
		deltas:              d.Deltas,
		latest:              d.Latest,
		events:              d.Events,
		devices:             d.Devices,
		vaults:              d.Vaults,
		blobs:               d.Blobs,
		blobQueue:           d.BlobQueue,
		publisher:           d.Publisher,
		tx:                  d.Tx,
		authz:               authorizer,
		collabDocs:          d.CollabDocs,
		shares:              d.Shares,
		access:              d.Access,
		deltaPolicy:         policy,
		maxFileSize:         d.MaxFileSize,
		maxSnapshotsPerFile: d.MaxSnapshotsPerFile,
		enforceQuota:        d.EnforceQuota,
	}
}

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

type FileInfo struct {
	VaultID        uuid.UUID
	FilePath       string
	Version        int
	SnapshotID     uuid.UUID
	Checksum       string
	SizeBytes      int64
	ContentType    string
	Deleted        bool
	NeedsSnapshot  bool
	LastModifiedBy uuid.UUID
	LastDeviceID   uuid.UUID
	CreatedBy      uuid.UUID
	UpdatedAt      time.Time
	CreatedAt      time.Time
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
		if txErr = uc.addStorage(ctx, input.VaultID, sizeBytes-freed); txErr != nil {
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
	if err != nil || duplicate != nil {
		uc.discardUpload(ctx, blobKey)
	}
	if err != nil {
		return nil, err
	}
	if duplicate != nil {
		return fileInfoFromLatest(duplicate), nil
	}

	uc.publishSSE(syncEvent)
	uc.cleanupDeltasAfterSnapshot(ctx, input.VaultID, input.FilePath, fl.CurrentVersion)

	info := fileInfoFromLatest(fl)
	info.SnapshotID = snapshotID
	return info, nil
}

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

func isSameContent(latest *domain.FileLatest, checksum string) bool {
	return checksum != "" && !latest.Deleted && latest.Checksum == checksum
}

func fileInfoFromLatest(fl *domain.FileLatest) *FileInfo {
	return &FileInfo{
		VaultID:        fl.VaultID,
		FilePath:       fl.FilePath,
		Version:        fl.CurrentVersion,
		Checksum:       fl.Checksum,
		SizeBytes:      fl.SizeBytes,
		ContentType:    fl.ContentType,
		Deleted:        fl.Deleted,
		LastModifiedBy: fl.LastModifiedBy,
		LastDeviceID:   fl.LastDeviceID,
		CreatedBy:      fl.CreatedBy,
		UpdatedAt:      fl.UpdatedAt,
		CreatedAt:      fl.CreatedAt,
	}
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
	if uc.maxFileSize > 0 && deltaSize > uc.maxFileSize {
		return nil, domain.ErrFileTooLarge
	}

	now := time.Now()
	var (
		existing  *domain.FileLatest
		fl        *domain.FileLatest
		syncEvent *domain.SyncEvent
	)

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		var txErr error
		existing, txErr = uc.latest.GetForUpdate(ctx, input.VaultID, input.FilePath)
		if txErr != nil {
			return txErr
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
	uc.publishSSE(syncEvent)

	info := fileInfoFromLatest(fl)
	info.NeedsSnapshot = uc.needsSnapshot(fl, existing.SizeBytes)
	return info, nil
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

type DownloadResult struct {
	Snapshot    *domain.FileSnapshot
	FileVersion int
	Reader      io.ReadCloser
}

func (uc *Service) DownloadSnapshot(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string, version int) (*DownloadResult, error) {
	if _, err := uc.authz.RequirePath(ctx, vaultID, userID, filePath, domain.PermFileRead); err != nil {
		return nil, err
	}

	var snapshot *domain.FileSnapshot
	fileVersion := version
	if version > 0 {
		found, err := uc.snapshots.GetAtOrBefore(ctx, vaultID, filePath, version)
		if err != nil {
			return nil, err
		}
		snapshot = found
	} else {
		latest, err := uc.latest.Get(ctx, vaultID, filePath)
		if err != nil {
			return nil, err
		}
		found, err := uc.snapshots.GetLatest(ctx, vaultID, filePath)
		if err != nil {
			return nil, err
		}
		snapshot, fileVersion = found, latest.CurrentVersion
	}

	reader, err := uc.blobs.Download(ctx, snapshot.EncryptedBlobKey)
	if err != nil {
		return nil, err
	}
	return &DownloadResult{Snapshot: snapshot, FileVersion: fileVersion, Reader: reader}, nil
}

type BatchItem struct {
	Path    string
	Version int
}

type BatchEntry struct {
	Path     string
	Version  int
	Snapshot *domain.FileSnapshot
	Err      error
}

type BatchPlan struct {
	Entries []BatchEntry
	blobs   BlobStorage
}

func (p *BatchPlan) Open(ctx context.Context, entry BatchEntry) (io.ReadCloser, error) {
	return p.blobs.Download(ctx, entry.Snapshot.EncryptedBlobKey)
}

func (uc *Service) PlanBatchDownload(ctx context.Context, userID, vaultID uuid.UUID, items []BatchItem) (*BatchPlan, error) {
	if len(items) == 0 || len(items) > BatchMaxItems {
		return nil, domain.ErrInvalidInput
	}
	access, err := uc.authz.Access(ctx, vaultID, userID, domain.PermFileRead)
	if err != nil {
		return nil, err
	}

	var latestPaths, versionedPaths []string
	var versions []int32
	for _, item := range items {
		if !access.Scope.CanRead(item.Path) {
			continue
		}
		if item.Version > 0 {
			versionedPaths = append(versionedPaths, item.Path)
			versions = append(versions, int32(item.Version))
		} else {
			latestPaths = append(latestPaths, item.Path)
		}
	}

	latest, err := uc.snapshots.ListLatestByPaths(ctx, vaultID, latestPaths)
	if err != nil {
		return nil, err
	}
	latestByPath := make(map[string]*domain.FileSnapshot, len(latest))
	for i := range latest {
		latestByPath[latest[i].FilePath] = &latest[i]
	}
	current, err := uc.latest.ListByPaths(ctx, vaultID, latestPaths)
	if err != nil {
		return nil, err
	}
	currentVersion := make(map[string]int, len(current))
	for _, file := range current {
		currentVersion[file.FilePath] = file.CurrentVersion
	}

	pinned, err := uc.snapshots.ListAtOrBefore(ctx, vaultID, versionedPaths, versions)
	if err != nil {
		return nil, err
	}

	plan := &BatchPlan{blobs: uc.blobs, Entries: make([]BatchEntry, 0, len(items))}
	var total int64
	versionedIndex := 0
	for _, item := range items {
		entry := BatchEntry{Path: item.Path, Version: item.Version}
		switch {
		case !access.Scope.CanRead(item.Path):
			entry.Err = domain.ErrInsufficientRole
		case item.Version > 0:
			if snapshot, ok := pinned[versionedIndex]; ok {
				entry.Snapshot = &snapshot
			}
			versionedIndex++
		default:
			entry.Snapshot = latestByPath[item.Path]
			entry.Version = currentVersion[item.Path]
		}
		if entry.Err == nil && entry.Snapshot == nil {
			entry.Err = domain.ErrNotFound
		}
		if entry.Snapshot != nil {
			total += entry.Snapshot.SizeBytes
		}
		plan.Entries = append(plan.Entries, entry)
	}
	if total > BatchMaxBytes {
		return nil, domain.ErrFileTooLarge
	}
	return plan, nil
}

type DownloadDeltasResult struct {
	Deltas []domain.FileDelta
}

func (uc *Service) DownloadDeltas(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string, sinceVersion int) (*DownloadDeltasResult, error) {
	if _, err := uc.authz.RequirePath(ctx, vaultID, userID, filePath, domain.PermFileRead); err != nil {
		return nil, err
	}

	deltas, err := uc.deltas.ListByFilePath(ctx, vaultID, filePath, sinceVersion)
	if err != nil {
		return nil, err
	}
	return &DownloadDeltasResult{Deltas: deltas}, nil
}

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
		existing, txErr := uc.latest.GetForUpdate(ctx, input.VaultID, input.OldPath)
		if txErr != nil {
			return txErr
		}
		if existing.Deleted {
			return domain.ErrNotFound
		}

		target, txErr := uc.latest.GetForUpdate(ctx, input.VaultID, input.NewPath)
		if txErr != nil && !errors.Is(txErr, domain.ErrNotFound) {
			return txErr
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
	if err := uc.addStorage(ctx, vaultID, -freed); err != nil {
		return err
	}
	_, err = uc.deltas.DeleteByFilePath(ctx, vaultID, path, math.MaxInt32)
	return err
}

type HistoryEntry struct {
	SnapshotID      uuid.UUID
	Version         int
	SizeBytes       int64
	Checksum        string
	AuthorID        uuid.UUID
	AuthorName      string
	AuthorAvatarURL string
	DeviceID        uuid.UUID
	DeviceName      string
	CreatedAt       time.Time
}

func (uc *Service) GetHistory(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string) ([]HistoryEntry, error) {
	if _, err := uc.authz.RequirePath(ctx, vaultID, userID, filePath, domain.PermFileRead); err != nil {
		return nil, err
	}

	rows, err := uc.snapshots.ListHistory(ctx, vaultID, filePath, maxHistoryEntries)
	if err != nil {
		return nil, err
	}

	result := make([]HistoryEntry, 0, len(rows))
	for _, h := range rows {
		author := domain.User{ID: h.CreatedBy, AvatarKey: h.AuthorAvatarKey}
		result = append(result, HistoryEntry{
			SnapshotID:      h.ID,
			Version:         h.Version,
			SizeBytes:       h.SizeBytes,
			Checksum:        h.Checksum,
			AuthorID:        h.CreatedBy,
			AuthorName:      h.AuthorName,
			AuthorAvatarURL: author.AvatarURL(),
			DeviceID:        h.DeviceID,
			DeviceName:      h.DeviceName,
			CreatedAt:       h.CreatedAt,
		})
	}
	return result, nil
}

type ListFilesInput struct {
	UserID         uuid.UUID
	VaultID        uuid.UUID
	IncludeDeleted bool
	After          string
	Limit          int
}

type FilePage struct {
	Files         []FileInfo
	NextCursor    string
	LatestEventID int64
	ScopeKey      string
}

func (uc *Service) ListFilesPage(ctx context.Context, input ListFilesInput) (*FilePage, error) {
	access, err := uc.authz.Access(ctx, input.VaultID, input.UserID, domain.PermFileRead)
	if err != nil {
		return nil, err
	}

	latestEventID, err := uc.events.LatestID(ctx, input.VaultID)
	if err != nil {
		return nil, err
	}

	limit := min(input.Limit, maxListPageSize)
	fetch := 0
	if limit > 0 {
		fetch = limit + 1
	}
	files, err := uc.latest.List(ctx, domain.FileListQuery{
		VaultID:        input.VaultID,
		IncludeDeleted: input.IncludeDeleted,
		AfterPath:      input.After,
		Limit:          fetch,
		Scope:          access.Scope,
	})
	if err != nil {
		return nil, err
	}

	page := &FilePage{LatestEventID: latestEventID, ScopeKey: access.Scope.Fingerprint()}
	if limit > 0 && len(files) > limit {
		files = files[:limit]
		page.NextCursor = files[len(files)-1].FilePath
	}
	page.Files = make([]FileInfo, 0, len(files))
	for i := range files {
		page.Files = append(page.Files, *fileInfoFromLatest(&files[i]))
	}
	return page, nil
}

func (uc *Service) LatestEventID(ctx context.Context, userID, vaultID uuid.UUID) (int64, string, error) {
	access, err := uc.authz.Access(ctx, vaultID, userID, domain.PermFileRead)
	if err != nil {
		return 0, "", err
	}
	latest, err := uc.events.LatestID(ctx, vaultID)
	return latest, access.Scope.Fingerprint(), err
}

func (uc *Service) ListFiles(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, includeDeleted bool) ([]FileInfo, error) {
	page, err := uc.ListFilesPage(ctx, ListFilesInput{UserID: userID, VaultID: vaultID, IncludeDeleted: includeDeleted})
	if err != nil {
		return nil, err
	}
	return page.Files, nil
}

func (uc *Service) GetFileInfo(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string) (*FileInfo, error) {
	if _, err := uc.authz.RequirePath(ctx, vaultID, userID, filePath, domain.PermFileRead); err != nil {
		return nil, err
	}

	fl, err := uc.latest.Get(ctx, vaultID, filePath)
	if err != nil {
		return nil, err
	}
	return fileInfoFromLatest(fl), nil
}

type ChangesResult struct {
	Events []domain.SyncEvent
}

func (uc *Service) ListChanges(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, sinceEventID int64, limit int) (*ChangesResult, error) {
	access, err := uc.authz.Access(ctx, vaultID, userID, domain.PermFileRead)
	if err != nil {
		return nil, err
	}

	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	events, err := uc.events.ListScoped(ctx, vaultID, sinceEventID, limit, access.Scope)
	if err != nil {
		return nil, err
	}
	return &ChangesResult{Events: events}, nil
}

type UpdateCursorInput struct {
	UserID      uuid.UUID
	DeviceID    uuid.UUID
	VaultID     uuid.UUID
	LastEventID int64
}

func (uc *Service) UpdateCursor(ctx context.Context, input UpdateCursorInput) error {
	if input.LastEventID < 0 {
		return domain.ErrInvalidInput
	}
	if _, err := uc.authz.Require(ctx, input.VaultID, input.UserID, domain.PermFileRead); err != nil {
		return err
	}
	if err := uc.devices.UpsertVaultCursor(ctx, input.DeviceID, input.VaultID, input.LastEventID); err != nil {
		return err
	}
	return uc.devices.UpdateLastSeen(ctx, input.DeviceID)
}

func blobKeyForSnapshot(vaultID uuid.UUID, snapshotID uuid.UUID) string {
	return fmt.Sprintf("vaults/%s/blobs/%s", vaultID, snapshotID)
}

func (uc *Service) UploadSnapshotFromBytes(ctx context.Context, input UploadSnapshotInput, data []byte) (*FileInfo, error) {
	input.Data = bytes.NewReader(data)
	input.SizeBytes = int64(len(data))
	return uc.UploadSnapshot(ctx, input)
}

func (uc *Service) cleanupDeltasAfterSnapshot(ctx context.Context, vaultID uuid.UUID, filePath string, snapshotVersion int) {
	_, _ = uc.deltas.DeleteByFilePath(ctx, vaultID, filePath, snapshotVersion)
}

func (uc *Service) publishSSE(event *domain.SyncEvent) {
	if uc.publisher == nil || event == nil {
		return
	}
	uc.publisher.PublishSyncEvent(event)
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

type BulkGetFileInfoInput struct {
	UserID    uuid.UUID
	VaultID   uuid.UUID
	FilePaths []string
}

func (uc *Service) BulkGetFileInfo(ctx context.Context, input BulkGetFileInfoInput) ([]FileInfo, error) {
	access, err := uc.authz.Access(ctx, input.VaultID, input.UserID, domain.PermFileRead)
	if err != nil {
		return nil, err
	}

	paths := input.FilePaths
	if !access.Scope.Unrestricted() {
		paths = make([]string, 0, len(input.FilePaths))
		for _, p := range input.FilePaths {
			if access.Scope.CanRead(p) {
				paths = append(paths, p)
			}
		}
	}

	files, err := uc.latest.ListByPaths(ctx, input.VaultID, paths)
	if err != nil {
		return nil, err
	}

	result := make([]FileInfo, 0, len(files))
	for i := range files {
		result = append(result, *fileInfoFromLatest(&files[i]))
	}
	return result, nil
}
