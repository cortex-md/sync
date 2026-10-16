package file

import (
	"context"
	"io"
	"strconv"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

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
		snapshot, fileVersion = found, found.Version
	} else {
		latest, err := uc.latest.Get(ctx, vaultID, filePath)
		if err != nil {
			return nil, err
		}
		if latest.Deleted {
			return nil, domain.ErrNotFound
		}
		var found *domain.FileSnapshot
		if latest.LatestSnapshotVersion > 0 {
			found, err = uc.snapshots.GetAtOrBefore(ctx, vaultID, filePath, latest.LatestSnapshotVersion)
		} else {
			found, err = uc.snapshots.GetLatest(ctx, vaultID, filePath)
		}
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
	Deltas     []domain.FileDelta
	NextCursor string
}

func (uc *Service) DownloadDeltas(ctx context.Context, userID uuid.UUID, vaultID uuid.UUID, filePath string, sinceVersion, limit int) (*DownloadDeltasResult, error) {
	if _, err := uc.authz.RequirePath(ctx, vaultID, userID, filePath, domain.PermFileRead); err != nil {
		return nil, err
	}

	if limit <= 0 || limit > maxDeltasPageSize {
		limit = maxDeltasPageSize
	}
	deltas, err := uc.deltas.ListByFilePath(ctx, vaultID, filePath, sinceVersion, limit+1)
	if err != nil {
		return nil, err
	}
	result := &DownloadDeltasResult{Deltas: deltas}
	if len(deltas) > limit {
		result.Deltas = deltas[:limit]
		result.NextCursor = strconv.Itoa(result.Deltas[limit-1].BaseVersion + 1)
	}
	return result, nil
}
