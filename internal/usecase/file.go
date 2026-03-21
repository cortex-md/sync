package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type FileUsecase struct {
	snapshots           port.FileSnapshotRepository
	deltas              port.FileDeltaRepository
	latest              port.FileLatestRepository
	events              port.SyncEventRepository
	members             port.VaultMemberRepository
	users               port.UserRepository
	devices             port.DeviceRepository
	blobs               port.BlobStorage
	broker              port.SSEBroker
	tx                  port.Transactor
	deltaPolicy         DeltaPolicy
	maxFileSize         int64
	maxSnapshotsPerFile int
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

func NewFileUsecase(
	snapshots port.FileSnapshotRepository,
	deltas port.FileDeltaRepository,
	latest port.FileLatestRepository,
	events port.SyncEventRepository,
	members port.VaultMemberRepository,
	users port.UserRepository,
	devices port.DeviceRepository,
	blobs port.BlobStorage,
	tx port.Transactor,
) *FileUsecase {
	return &FileUsecase{
		snapshots:   snapshots,
		deltas:      deltas,
		latest:      latest,
		events:      events,
		members:     members,
		users:       users,
		devices:     devices,
		blobs:       blobs,
		tx:          tx,
		deltaPolicy: DefaultDeltaPolicy(),
	}
}

func (uc *FileUsecase) SetBroker(broker port.SSEBroker) {
	uc.broker = broker
}

func (uc *FileUsecase) SetDeltaPolicy(policy DeltaPolicy) {
	uc.deltaPolicy = policy
}

func (uc *FileUsecase) SetMaxFileSize(maxBytes int64) {
	uc.maxFileSize = maxBytes
}

func (uc *FileUsecase) SetMaxSnapshotsPerFile(max int) {
	uc.maxSnapshotsPerFile = max
}

type UploadSnapshotInput struct {
	UserID      uuid.UUID
	DeviceID    uuid.UUID
	VaultID     uuid.UUID
	FilePath    string
	Checksum    string
	ContentType string
	SizeBytes   int64
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
	UpdatedAt      time.Time
	CreatedAt      time.Time
}

func (uc *FileUsecase) UploadSnapshot(ctx context.Context, input UploadSnapshotInput) (*FileInfo, error) {
	if input.FilePath == "" {
		return nil, domain.ErrInvalidInput
	}

	member, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !member.Role.CanWrite() {
		return nil, domain.ErrInsufficientRole
	}

	if uc.maxFileSize > 0 && input.SizeBytes > uc.maxFileSize {
		return nil, domain.ErrFileTooLarge
	}

	now := time.Now()
	snapshotID := uuid.New()

	var (
		version   int
		eventType domain.EventType
		fl        *domain.FileLatest
		existing  *domain.FileLatest
	)

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		var txErr error
		existing, txErr = uc.latest.Get(ctx, input.VaultID, input.FilePath)
		if txErr == nil {
			if input.Checksum != "" && existing.Checksum == input.Checksum {
				return nil
			}
			version = existing.CurrentVersion + 1
		} else if txErr == domain.ErrNotFound {
			version = 1
		} else {
			return txErr
		}

		blobKey := fmt.Sprintf("vaults/%s/files/%s/v%d/%s", input.VaultID, snapshotID, version, input.FilePath)

		if txErr = uc.blobs.Upload(ctx, blobKey, input.Data, input.SizeBytes, input.ContentType); txErr != nil {
			return txErr
		}

		snapshot := &domain.FileSnapshot{
			ID:               snapshotID,
			VaultID:          input.VaultID,
			FilePath:         input.FilePath,
			Version:          version,
			EncryptedBlobKey: blobKey,
			SizeBytes:        input.SizeBytes,
			Checksum:         input.Checksum,
			CreatedBy:        input.UserID,
			DeviceID:         input.DeviceID,
			CreatedAt:        now,
		}

		if txErr = uc.snapshots.Create(ctx, snapshot); txErr != nil {
			return txErr
		}

		contentType := input.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		eventType = domain.EventFileUpdated
		if version == 1 {
			eventType = domain.EventFileCreated
		}

		fl = &domain.FileLatest{
			VaultID:               input.VaultID,
			FilePath:              input.FilePath,
			CurrentVersion:        version,
			LatestSnapshotVersion: version,
			Checksum:              input.Checksum,
			SizeBytes:             input.SizeBytes,
			ContentType:           contentType,
			Deleted:               false,
			LastModifiedBy:        input.UserID,
			LastDeviceID:          input.DeviceID,
			UpdatedAt:             now,
			CreatedAt:             now,
		}

		if existing != nil {
			fl.CreatedAt = existing.CreatedAt
		}

		if txErr = uc.latest.Upsert(ctx, fl); txErr != nil {
			return txErr
		}

		syncEvent := &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: eventType,
			FilePath:  input.FilePath,
			Version:   version,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{"checksum": input.Checksum, "size_bytes": input.SizeBytes},
			CreatedAt: now,
		}

		if txErr = uc.events.Create(ctx, syncEvent); txErr != nil {
			return txErr
		}
		uc.publishSSE(syncEvent)
		return nil
	}); err != nil {
		return nil, err
	}

	if version == 0 {
		return &FileInfo{
			VaultID:        existing.VaultID,
			FilePath:       existing.FilePath,
			Version:        existing.CurrentVersion,
			Checksum:       existing.Checksum,
			SizeBytes:      existing.SizeBytes,
			ContentType:    existing.ContentType,
			Deleted:        existing.Deleted,
			LastModifiedBy: existing.LastModifiedBy,
			LastDeviceID:   existing.LastDeviceID,
			UpdatedAt:      existing.UpdatedAt,
			CreatedAt:      existing.CreatedAt,
		}, nil
	}

	uc.cleanupDeltasAfterSnapshot(ctx, input.VaultID, input.FilePath, version)
	uc.pruneOldSnapshots(ctx, input.VaultID, input.FilePath)

	return &FileInfo{
		VaultID:        input.VaultID,
		FilePath:       input.FilePath,
		Version:        version,
		SnapshotID:     snapshotID,
		Checksum:       input.Checksum,
		SizeBytes:      input.SizeBytes,
		ContentType:    fl.ContentType,
		Deleted:        false,
		LastModifiedBy: input.UserID,
		LastDeviceID:   input.DeviceID,
		UpdatedAt:      now,
		CreatedAt:      fl.CreatedAt,
	}, nil
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

func (uc *FileUsecase) UploadDelta(ctx context.Context, input UploadDeltaInput) (*FileInfo, error) {
	if input.FilePath == "" {
		return nil, domain.ErrInvalidInput
	}

	if len(input.EncryptedData) == 0 {
		return nil, domain.ErrInvalidInput
	}

	member, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !member.Role.CanWrite() {
		return nil, domain.ErrInsufficientRole
	}

	if uc.maxFileSize > 0 && int64(len(input.EncryptedData)) > uc.maxFileSize {
		return nil, domain.ErrFileTooLarge
	}

	existing, err := uc.latest.Get(ctx, input.VaultID, input.FilePath)
	if err != nil {
		return nil, err
	}

	if existing.CurrentVersion != input.BaseVersion {
		return nil, domain.ErrConflict
	}

	now := time.Now()
	targetVersion := existing.CurrentVersion + 1

	delta := &domain.FileDelta{
		ID:             uuid.New(),
		VaultID:        input.VaultID,
		FilePath:       input.FilePath,
		BaseVersion:    input.BaseVersion,
		TargetVersion:  targetVersion,
		EncryptedDelta: input.EncryptedData,
		SizeBytes:      int64(len(input.EncryptedData)),
		CreatedBy:      input.UserID,
		DeviceID:       input.DeviceID,
		CreatedAt:      now,
	}

	if err := uc.deltas.Create(ctx, delta); err != nil {
		return nil, err
	}

	fl := &domain.FileLatest{
		VaultID:               input.VaultID,
		FilePath:              input.FilePath,
		CurrentVersion:        targetVersion,
		LatestSnapshotVersion: existing.LatestSnapshotVersion,
		Checksum:              input.Checksum,
		SizeBytes:             input.SizeBytes,
		ContentType:           existing.ContentType,
		Deleted:               false,
		LastModifiedBy:        input.UserID,
		LastDeviceID:          input.DeviceID,
		UpdatedAt:             now,
		CreatedAt:             existing.CreatedAt,
	}

	if err := uc.latest.Upsert(ctx, fl); err != nil {
		return nil, err
	}

	syncEvent := &domain.SyncEvent{
		VaultID:   input.VaultID,
		EventType: domain.EventFileUpdated,
		FilePath:  input.FilePath,
		Version:   targetVersion,
		ActorID:   input.UserID,
		DeviceID:  input.DeviceID,
		Metadata:  map[string]any{"checksum": input.Checksum, "delta": true, "base_version": input.BaseVersion},
		CreatedAt: now,
	}

	if err := uc.events.Create(ctx, syncEvent); err != nil {
		return nil, err
	}
	uc.publishSSE(syncEvent)

	needsSnapshot := uc.evaluateSnapshotNeed(ctx, input.VaultID, input.FilePath, existing.LatestSnapshotVersion, existing.SizeBytes)

	return &FileInfo{
		VaultID:        input.VaultID,
		FilePath:       input.FilePath,
		Version:        targetVersion,
		Checksum:       input.Checksum,
		SizeBytes:      input.SizeBytes,
		ContentType:    fl.ContentType,
		Deleted:        false,
		NeedsSnapshot:  needsSnapshot,
		LastModifiedBy: input.UserID,
		LastDeviceID:   input.DeviceID,
		UpdatedAt:      now,
		CreatedAt:      existing.CreatedAt,
	}, nil
}

type DownloadResult struct {
	Snapshot *domain.FileSnapshot
	Reader   io.ReadCloser
}

func (uc *FileUsecase) DownloadSnapshot(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string, version int) (*DownloadResult, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	var snapshot *domain.FileSnapshot

	if version > 0 {
		snapshot, err = uc.snapshots.GetByVersion(ctx, vaultID, filePath, version)
	} else {
		snapshot, err = uc.snapshots.GetLatest(ctx, vaultID, filePath)
	}

	if err != nil {
		return nil, err
	}

	reader, err := uc.blobs.Download(ctx, snapshot.EncryptedBlobKey)
	if err != nil {
		return nil, err
	}

	return &DownloadResult{
		Snapshot: snapshot,
		Reader:   reader,
	}, nil
}

type DownloadDeltasResult struct {
	Deltas []domain.FileDelta
}

func (uc *FileUsecase) DownloadDeltas(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string, sinceVersion int) (*DownloadDeltasResult, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	deltas, err := uc.deltas.ListByFilePath(ctx, vaultID, filePath, sinceVersion)
	if err != nil {
		return nil, err
	}

	return &DownloadDeltasResult{Deltas: deltas}, nil
}

type DeleteFileInput struct {
	UserID   uuid.UUID
	DeviceID uuid.UUID
	VaultID  uuid.UUID
	FilePath string
}

func (uc *FileUsecase) DeleteFile(ctx context.Context, input DeleteFileInput) error {
	if input.FilePath == "" {
		return domain.ErrInvalidInput
	}

	member, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrVaultAccessDenied
		}
		return err
	}

	if !member.Role.CanWrite() {
		return domain.ErrInsufficientRole
	}

	existing, err := uc.latest.Get(ctx, input.VaultID, input.FilePath)
	if err != nil {
		return err
	}

	if existing.Deleted {
		return domain.ErrNotFound
	}

	now := time.Now()
	existing.Deleted = true
	existing.LastModifiedBy = input.UserID
	existing.LastDeviceID = input.DeviceID
	existing.UpdatedAt = now
	existing.CurrentVersion = existing.CurrentVersion + 1

	if err := uc.latest.Upsert(ctx, existing); err != nil {
		return err
	}

	syncEvent := &domain.SyncEvent{
		VaultID:   input.VaultID,
		EventType: domain.EventFileDeleted,
		FilePath:  input.FilePath,
		Version:   existing.CurrentVersion,
		ActorID:   input.UserID,
		DeviceID:  input.DeviceID,
		Metadata:  map[string]any{},
		CreatedAt: now,
	}

	if err := uc.events.Create(ctx, syncEvent); err != nil {
		return err
	}
	uc.publishSSE(syncEvent)
	return nil
}

type RestoreFileInput struct {
	UserID   uuid.UUID
	DeviceID uuid.UUID
	VaultID  uuid.UUID
	FilePath string
}

func (uc *FileUsecase) RestoreFile(ctx context.Context, input RestoreFileInput) error {
	if input.FilePath == "" {
		return domain.ErrInvalidInput
	}

	member, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrVaultAccessDenied
		}
		return err
	}

	if !member.Role.CanWrite() {
		return domain.ErrInsufficientRole
	}

	existing, err := uc.latest.Get(ctx, input.VaultID, input.FilePath)
	if err != nil {
		return err
	}

	if !existing.Deleted {
		return domain.ErrInvalidInput
	}

	now := time.Now()
	existing.Deleted = false
	existing.LastModifiedBy = input.UserID
	existing.LastDeviceID = input.DeviceID
	existing.UpdatedAt = now
	existing.CurrentVersion = existing.CurrentVersion + 1

	if err := uc.latest.Upsert(ctx, existing); err != nil {
		return err
	}

	syncEvent := &domain.SyncEvent{
		VaultID:   input.VaultID,
		EventType: domain.EventFileCreated,
		FilePath:  input.FilePath,
		Version:   existing.CurrentVersion,
		ActorID:   input.UserID,
		DeviceID:  input.DeviceID,
		Metadata:  map[string]any{},
		CreatedAt: now,
	}

	if err := uc.events.Create(ctx, syncEvent); err != nil {
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

func (uc *FileUsecase) RenameFile(ctx context.Context, input RenameFileInput) (*FileInfo, error) {
	if input.OldPath == "" || input.NewPath == "" {
		return nil, domain.ErrInvalidInput
	}

	if input.OldPath == input.NewPath {
		return nil, domain.ErrInvalidInput
	}

	member, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if !member.Role.CanWrite() {
		return nil, domain.ErrInsufficientRole
	}

	now := time.Now()
	var result *FileInfo

	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		existing, txErr := uc.latest.Get(ctx, input.VaultID, input.OldPath)
		if txErr != nil {
			return txErr
		}

		if existing.Deleted {
			return domain.ErrNotFound
		}

		_, txErr = uc.latest.Get(ctx, input.VaultID, input.NewPath)
		if txErr == nil {
			return domain.ErrAlreadyExists
		} else if txErr != domain.ErrNotFound {
			return txErr
		}

		newLatest := &domain.FileLatest{
			VaultID:               input.VaultID,
			FilePath:              input.NewPath,
			CurrentVersion:        existing.CurrentVersion + 1,
			LatestSnapshotVersion: existing.LatestSnapshotVersion,
			Checksum:              existing.Checksum,
			SizeBytes:             existing.SizeBytes,
			ContentType:           existing.ContentType,
			Deleted:               false,
			LastModifiedBy:        input.UserID,
			LastDeviceID:          input.DeviceID,
			UpdatedAt:             now,
			CreatedAt:             existing.CreatedAt,
		}

		if txErr = uc.latest.Upsert(ctx, newLatest); txErr != nil {
			return txErr
		}

		existing.Deleted = true
		existing.LastModifiedBy = input.UserID
		existing.LastDeviceID = input.DeviceID
		existing.UpdatedAt = now
		existing.CurrentVersion = existing.CurrentVersion + 1

		if txErr = uc.latest.Upsert(ctx, existing); txErr != nil {
			return txErr
		}

		syncEvent := &domain.SyncEvent{
			VaultID:   input.VaultID,
			EventType: domain.EventFileRenamed,
			FilePath:  input.NewPath,
			Version:   newLatest.CurrentVersion,
			ActorID:   input.UserID,
			DeviceID:  input.DeviceID,
			Metadata:  map[string]any{"old_path": input.OldPath, "new_path": input.NewPath},
			CreatedAt: now,
		}

		if txErr = uc.events.Create(ctx, syncEvent); txErr != nil {
			return txErr
		}
		uc.publishSSE(syncEvent)

		result = &FileInfo{
			VaultID:        input.VaultID,
			FilePath:       input.NewPath,
			Version:        newLatest.CurrentVersion,
			Checksum:       newLatest.Checksum,
			SizeBytes:      newLatest.SizeBytes,
			ContentType:    newLatest.ContentType,
			Deleted:        false,
			LastModifiedBy: input.UserID,
			LastDeviceID:   input.DeviceID,
			UpdatedAt:      now,
			CreatedAt:      existing.CreatedAt,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

type HistoryEntry struct {
	SnapshotID uuid.UUID
	Version    int
	SizeBytes  int64
	Checksum   string
	AuthorID   uuid.UUID
	AuthorName string
	DeviceID   uuid.UUID
	DeviceName string
	CreatedAt  time.Time
}

func (uc *FileUsecase) GetHistory(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string) ([]HistoryEntry, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	snapshots, err := uc.snapshots.ListByFilePath(ctx, vaultID, filePath)
	if err != nil {
		return nil, err
	}

	result := make([]HistoryEntry, 0, len(snapshots))
	for _, s := range snapshots {
		entry := HistoryEntry{
			SnapshotID: s.ID,
			Version:    s.Version,
			SizeBytes:  s.SizeBytes,
			Checksum:   s.Checksum,
			AuthorID:   s.CreatedBy,
			DeviceID:   s.DeviceID,
			CreatedAt:  s.CreatedAt,
		}

		if author, userErr := uc.users.GetByID(ctx, s.CreatedBy); userErr == nil {
			entry.AuthorName = author.DisplayName
		}

		if device, deviceErr := uc.devices.GetByID(ctx, s.DeviceID); deviceErr == nil {
			entry.DeviceName = device.DeviceName
		}

		result = append(result, entry)
	}

	return result, nil
}

func (uc *FileUsecase) ListFiles(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, includeDeleted bool) ([]FileInfo, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	files, err := uc.latest.ListByVaultID(ctx, vaultID, 0)
	if err != nil {
		return nil, err
	}

	result := make([]FileInfo, 0, len(files))
	for _, f := range files {
		if !includeDeleted && f.Deleted {
			continue
		}
		result = append(result, FileInfo{
			VaultID:        f.VaultID,
			FilePath:       f.FilePath,
			Version:        f.CurrentVersion,
			Checksum:       f.Checksum,
			SizeBytes:      f.SizeBytes,
			ContentType:    f.ContentType,
			Deleted:        f.Deleted,
			LastModifiedBy: f.LastModifiedBy,
			LastDeviceID:   f.LastDeviceID,
			UpdatedAt:      f.UpdatedAt,
			CreatedAt:      f.CreatedAt,
		})
	}

	return result, nil
}

func (uc *FileUsecase) GetFileInfo(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string) (*FileInfo, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	fl, err := uc.latest.Get(ctx, vaultID, filePath)
	if err != nil {
		return nil, err
	}

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
		UpdatedAt:      fl.UpdatedAt,
		CreatedAt:      fl.CreatedAt,
	}, nil
}

type ChangesResult struct {
	Events []domain.SyncEvent
}

func (uc *FileUsecase) ListChanges(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, sinceEventID int64, limit int) (*ChangesResult, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, vaultID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	events, err := uc.events.ListByVaultID(ctx, vaultID, sinceEventID, limit)
	if err != nil {
		return nil, err
	}

	return &ChangesResult{Events: events}, nil
}

func blobKeyForSnapshot(vaultID uuid.UUID, snapshotID uuid.UUID, version int, filePath string) string {
	return fmt.Sprintf("vaults/%s/files/%s/v%d/%s", vaultID, snapshotID, version, filePath)
}

func (uc *FileUsecase) UploadSnapshotFromBytes(ctx context.Context, input UploadSnapshotInput, data []byte) (*FileInfo, error) {
	input.Data = bytes.NewReader(data)
	input.SizeBytes = int64(len(data))
	return uc.UploadSnapshot(ctx, input)
}

func (uc *FileUsecase) evaluateSnapshotNeed(ctx context.Context, vaultID uuid.UUID, filePath string, latestSnapshotVersion int, lastSnapshotSizeBytes int64) bool {
	deltas, err := uc.deltas.ListByFilePath(ctx, vaultID, filePath, latestSnapshotVersion)
	if err != nil {
		return false
	}

	deltaCount := len(deltas)
	if uc.deltaPolicy.MaxDeltasBeforeSnapshot > 0 && deltaCount >= uc.deltaPolicy.MaxDeltasBeforeSnapshot {
		return true
	}

	if uc.deltaPolicy.MaxDeltaSizeRatio > 0 && lastSnapshotSizeBytes > 0 {
		var totalDeltaSize int64
		for _, d := range deltas {
			totalDeltaSize += d.SizeBytes
		}
		ratio := float64(totalDeltaSize) / float64(lastSnapshotSizeBytes)
		if ratio >= uc.deltaPolicy.MaxDeltaSizeRatio {
			return true
		}
	}

	return false
}

func (uc *FileUsecase) cleanupDeltasAfterSnapshot(ctx context.Context, vaultID uuid.UUID, filePath string, snapshotVersion int) {
	uc.deltas.DeleteByFilePath(ctx, vaultID, filePath, snapshotVersion)
}

func (uc *FileUsecase) publishSSE(event *domain.SyncEvent) {
	if uc.broker == nil {
		return
	}

	data := map[string]any{
		"vault_uuid": event.VaultID.String(),
		"file_path":  event.FilePath,
		"version":    event.Version,
		"actor_id":   event.ActorID.String(),
		"device_id":  event.DeviceID.String(),
	}
	for k, v := range event.Metadata {
		data[k] = v
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	uc.broker.Publish(event.VaultID, port.SSEEvent{
		ID:        strconv.FormatInt(event.ID, 10),
		EventType: string(event.EventType),
		Data:      string(jsonData),
	})
}

func (uc *FileUsecase) pruneOldSnapshots(ctx context.Context, vaultID uuid.UUID, filePath string) {
	if uc.maxSnapshotsPerFile <= 0 {
		return
	}

	deleted, err := uc.snapshots.DeleteOlderVersions(ctx, vaultID, filePath, uc.maxSnapshotsPerFile)
	if err != nil {
		return
	}

	for _, s := range deleted {
		uc.blobs.Delete(ctx, s.EncryptedBlobKey)
	}
}

type BulkGetFileInfoInput struct {
	UserID    uuid.UUID
	VaultID   uuid.UUID
	FilePaths []string
}

func (uc *FileUsecase) BulkGetFileInfo(ctx context.Context, input BulkGetFileInfoInput) ([]FileInfo, error) {
	_, err := uc.members.GetByVaultAndUser(ctx, input.VaultID, input.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrVaultAccessDenied
		}
		return nil, err
	}

	result := make([]FileInfo, 0, len(input.FilePaths))
	for _, path := range input.FilePaths {
		fl, err := uc.latest.Get(ctx, input.VaultID, path)
		if err == domain.ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, FileInfo{
			VaultID:        fl.VaultID,
			FilePath:       fl.FilePath,
			Version:        fl.CurrentVersion,
			Checksum:       fl.Checksum,
			SizeBytes:      fl.SizeBytes,
			ContentType:    fl.ContentType,
			Deleted:        fl.Deleted,
			LastModifiedBy: fl.LastModifiedBy,
			LastDeviceID:   fl.LastDeviceID,
			UpdatedAt:      fl.UpdatedAt,
			CreatedAt:      fl.CreatedAt,
		})
	}

	return result, nil
}
