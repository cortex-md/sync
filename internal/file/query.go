package file

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

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

	limit := input.Limit
	if limit <= 0 {
		limit = defaultListPageSize
	}
	limit = min(limit, maxListPageSize)
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
	Events  []domain.SyncEvent
	HasMore bool
}

func (uc *Service) ListChanges(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, sinceEventID int64, limit int) (*ChangesResult, error) {
	access, err := uc.authz.Access(ctx, vaultID, userID, domain.PermFileRead)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 1000)

	if sinceEventID > 0 {
		prunedThrough, err := uc.events.PrunedThrough(ctx)
		if err != nil {
			return nil, err
		}
		if sinceEventID < prunedThrough {
			return nil, domain.ErrCursorExpired
		}
	}

	events, err := uc.events.ListScoped(ctx, vaultID, sinceEventID, limit, access.Scope)
	if err != nil {
		return nil, err
	}
	return &ChangesResult{Events: events, HasMore: len(events) == limit}, nil
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
