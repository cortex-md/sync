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

func (r *FileDeltaRepository) ListByFilePath(_ context.Context, vaultID uuid.UUID, filePath string, sinceVersion int) ([]domain.FileDelta, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.FileDelta
	for _, d := range r.deltas {
		if d.VaultID == vaultID && d.FilePath == filePath && d.BaseVersion >= sinceVersion {
			result = append(result, *d)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BaseVersion < result[j].BaseVersion })
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
