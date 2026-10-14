package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type FileSnapshotRepository struct {
	mu        sync.RWMutex
	snapshots map[uuid.UUID]*domain.FileSnapshot
	users     *UserRepository
	devices   *DeviceRepository
}

func (r *FileSnapshotRepository) ResolveAuthorsWith(users *UserRepository, devices *DeviceRepository) {
	r.users = users
	r.devices = devices
}

func (r *FileSnapshotRepository) ListLatestByPaths(ctx context.Context, vaultID uuid.UUID, paths []string) ([]domain.FileSnapshot, error) {
	var result []domain.FileSnapshot
	for _, path := range paths {
		if s, err := r.GetLatest(ctx, vaultID, path); err == nil {
			result = append(result, *s)
		}
	}
	return result, nil
}

func (r *FileSnapshotRepository) ListAtOrBefore(ctx context.Context, vaultID uuid.UUID, paths []string, versions []int32) (map[int]domain.FileSnapshot, error) {
	pinned := make(map[int]domain.FileSnapshot)
	for i, path := range paths {
		if s, err := r.GetAtOrBefore(ctx, vaultID, path, int(versions[i])); err == nil {
			pinned[i] = *s
		}
	}
	return pinned, nil
}

func (r *FileSnapshotRepository) GetAtOrBefore(_ context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var best *domain.FileSnapshot
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath && s.Version <= version && (best == nil || s.Version > best.Version) {
			best = s
		}
	}
	if best == nil {
		return nil, domain.ErrNotFound
	}
	result := *best
	return &result, nil
}

func (r *FileSnapshotRepository) ListHistory(ctx context.Context, vaultID uuid.UUID, filePath string, limit int) ([]domain.HistoryEntry, error) {
	snapshots, _ := r.ListByFilePath(ctx, vaultID, filePath)
	if limit > 0 && len(snapshots) > limit {
		snapshots = snapshots[len(snapshots)-limit:]
	}
	entries := make([]domain.HistoryEntry, 0, len(snapshots))
	for _, s := range snapshots {
		entry := domain.HistoryEntry{FileSnapshot: s}
		if r.users != nil {
			if u, err := r.users.GetByID(ctx, s.CreatedBy); err == nil {
				entry.AuthorName, entry.AuthorAvatarKey = u.DisplayName, u.AvatarKey
			}
		}
		if r.devices != nil {
			if d, err := r.devices.GetByID(ctx, s.DeviceID); err == nil {
				entry.DeviceName = d.DeviceName
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func NewFileSnapshotRepository() *FileSnapshotRepository {
	return &FileSnapshotRepository{
		snapshots: make(map[uuid.UUID]*domain.FileSnapshot),
	}
}

func (r *FileSnapshotRepository) Create(_ context.Context, snapshot *domain.FileSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *snapshot
	r.snapshots[snapshot.ID] = &stored
	return nil
}

func (r *FileSnapshotRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.FileSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot, exists := r.snapshots[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *snapshot
	return &result, nil
}

func (r *FileSnapshotRepository) GetLatest(_ context.Context, vaultID uuid.UUID, filePath string) (*domain.FileSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest *domain.FileSnapshot
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath {
			if latest == nil || s.Version > latest.Version {
				cp := *s
				latest = &cp
			}
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}

func (r *FileSnapshotRepository) GetByVersion(_ context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath && s.Version == version {
			result := *s
			return &result, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *FileSnapshotRepository) ListByFilePath(_ context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.FileSnapshot
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath {
			result = append(result, *s)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	return result, nil
}

func (r *FileSnapshotRepository) DeleteOlderVersions(_ context.Context, vaultID uuid.UUID, filePath string, keepCount int) ([]domain.FileSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matching []*domain.FileSnapshot
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath {
			cp := *s
			matching = append(matching, &cp)
		}
	}
	sort.Slice(matching, func(i, j int) bool { return matching[i].Version > matching[j].Version })

	if len(matching) <= keepCount {
		return nil, nil
	}

	var deleted []domain.FileSnapshot
	for _, s := range matching[keepCount:] {
		deleted = append(deleted, *s)
		delete(r.snapshots, s.ID)
	}
	return deleted, nil
}

func (r *FileSnapshotRepository) DeleteByFilePath(_ context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var deleted []domain.FileSnapshot
	for id, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath {
			deleted = append(deleted, *s)
			delete(r.snapshots, id)
		}
	}
	return deleted, nil
}

func (r *FileSnapshotRepository) MovePath(_ context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == oldPath {
			s.FilePath = newPath
		}
	}
	return nil
}

func (r *FileSnapshotRepository) ListBlobKeysByVault(_ context.Context, vaultID uuid.UUID) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var keys []string
	for _, s := range r.snapshots {
		if s.VaultID == vaultID {
			keys = append(keys, s.EncryptedBlobKey)
		}
	}
	return keys, nil
}

func (r *FileSnapshotRepository) CountForFile(vaultID uuid.UUID, filePath string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, s := range r.snapshots {
		if s.VaultID == vaultID && s.FilePath == filePath {
			count++
		}
	}
	return count
}

type FileDeltaRepository struct {
	mu     sync.RWMutex
	deltas map[uuid.UUID]*domain.FileDelta
}

func NewFileDeltaRepository() *FileDeltaRepository {
	return &FileDeltaRepository{
		deltas: make(map[uuid.UUID]*domain.FileDelta),
	}
}

func (r *FileDeltaRepository) Create(_ context.Context, delta *domain.FileDelta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *delta
	r.deltas[delta.ID] = &stored
	return nil
}

func (r *FileDeltaRepository) ListByFilePath(_ context.Context, vaultID uuid.UUID, filePath string, sinceVersion, limit int) ([]domain.FileDelta, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.FileDelta
	for _, d := range r.deltas {
		if d.VaultID == vaultID && d.FilePath == filePath && d.BaseVersion >= sinceVersion {
			result = append(result, *d)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BaseVersion < result[j].BaseVersion })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (r *FileDeltaRepository) DeleteByFilePath(_ context.Context, vaultID uuid.UUID, filePath string, beforeVersion int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for id, d := range r.deltas {
		if d.VaultID == vaultID && d.FilePath == filePath && d.TargetVersion < beforeVersion {
			delete(r.deltas, id)
			count++
		}
	}
	return count, nil
}

func (r *FileDeltaRepository) MovePath(_ context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.deltas {
		if d.VaultID == vaultID && d.FilePath == oldPath {
			d.FilePath = newPath
		}
	}
	return nil
}

type FileLatestRepository struct {
	mu    sync.RWMutex
	files map[string]*domain.FileLatest
}

func NewFileLatestRepository() *FileLatestRepository {
	return &FileLatestRepository{
		files: make(map[string]*domain.FileLatest),
	}
}

func fileKey(vaultID uuid.UUID, filePath string) string {
	return fmt.Sprintf("%s:%s", vaultID.String(), filePath)
}

func (r *FileLatestRepository) Upsert(_ context.Context, latest *domain.FileLatest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *latest
	r.files[fileKey(latest.VaultID, latest.FilePath)] = &stored
	return nil
}

func (r *FileLatestRepository) Get(_ context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, exists := r.files[fileKey(vaultID, filePath)]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *f
	return &result, nil
}

func (r *FileLatestRepository) GetForUpdate(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error) {
	return r.Get(ctx, vaultID, filePath)
}

func (r *FileLatestRepository) ListByPaths(_ context.Context, vaultID uuid.UUID, filePaths []string) ([]domain.FileLatest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.FileLatest, 0, len(filePaths))
	for _, filePath := range filePaths {
		f, exists := r.files[fileKey(vaultID, filePath)]
		if !exists {
			continue
		}
		result = append(result, *f)
	}
	return result, nil
}

func (r *FileLatestRepository) List(_ context.Context, query domain.FileListQuery) ([]domain.FileLatest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.FileLatest
	for _, f := range r.files {
		if f.VaultID != query.VaultID || (!query.IncludeDeleted && f.Deleted) || f.FilePath <= query.AfterPath {
			continue
		}
		if !query.Scope.All && !query.Scope.CanRead(f.FilePath) {
			continue
		}
		result = append(result, *f)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FilePath < result[j].FilePath })
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}
	return result, nil
}

func (r *FileLatestRepository) ListByVaultID(_ context.Context, vaultID uuid.UUID, sinceVersion int) ([]domain.FileLatest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.FileLatest
	for _, f := range r.files {
		if f.VaultID == vaultID && f.CurrentVersion > sinceVersion {
			result = append(result, *f)
		}
	}
	return result, nil
}

func (r *FileLatestRepository) Delete(_ context.Context, vaultID uuid.UUID, filePath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fileKey(vaultID, filePath)
	if _, exists := r.files[key]; !exists {
		return domain.ErrNotFound
	}
	delete(r.files, key)
	return nil
}

type SyncEventRepository struct {
	mu     sync.RWMutex
	events []domain.SyncEvent
	nextID int64
}

func NewSyncEventRepository() *SyncEventRepository {
	return &SyncEventRepository{
		nextID: 1,
	}
}

func (r *SyncEventRepository) Create(_ context.Context, event *domain.SyncEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	event.ID = r.nextID
	r.nextID++
	stored := *event
	r.events = append(r.events, stored)
	return nil
}

func (r *SyncEventRepository) GetByID(_ context.Context, id int64) (*domain.SyncEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.events {
		if e.ID == id {
			result := e
			return &result, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *SyncEventRepository) ListByVaultID(_ context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.SyncEvent
	for _, e := range r.events {
		if e.VaultID == vaultID && e.ID > sinceID {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (r *SyncEventRepository) ListScoped(_ context.Context, vaultID uuid.UUID, sinceID int64, limit int, scope domain.Scope) ([]domain.SyncEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.SyncEvent
	for _, e := range r.events {
		if e.VaultID != vaultID || e.ID <= sinceID {
			continue
		}
		oldPath, _ := e.Metadata["old_path"].(string)
		if !scope.All && !scope.CanRead(e.FilePath) && !(oldPath != "" && scope.CanRead(oldPath)) {
			continue
		}
		result = append(result, e)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (r *SyncEventRepository) LatestID(_ context.Context, vaultID uuid.UUID) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest int64
	for _, e := range r.events {
		if e.VaultID == vaultID && e.ID > latest {
			latest = e.ID
		}
	}
	return latest, nil
}

func (r *SyncEventRepository) DeleteOlderThan(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept []domain.SyncEvent
	var count int64
	for _, e := range r.events {
		if e.CreatedAt.Before(before) {
			count++
		} else {
			kept = append(kept, e)
		}
	}
	r.events = kept
	return count, nil
}

func (r *FileSnapshotRepository) ListByIDs(_ context.Context, vaultID uuid.UUID, ids []uuid.UUID) ([]domain.FileSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.FileSnapshot
	for _, id := range ids {
		if s, ok := r.snapshots[id]; ok && s.VaultID == vaultID {
			result = append(result, *s)
		}
	}
	return result, nil
}
