package postgres

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const scopedPageSize = 1000

func scopeParams(scope domain.Scope) (all bool, exact []string, patterns []string) {
	all, exactPaths, prefixes := scope.ReadFilter()
	if all {
		return true, []string{}, []string{}
	}
	patterns = make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		patterns = append(patterns, escapeLike(prefix)+"%")
	}
	if exactPaths == nil {
		exactPaths = []string{}
	}
	return false, exactPaths, patterns
}

func eventVisible(scope domain.Scope, e domain.SyncEvent) bool {
	if scope.CanRead(e.FilePath) {
		return true
	}
	oldPath, _ := e.Metadata["old_path"].(string)
	return oldPath != "" && scope.CanRead(oldPath)
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func rowLimit(limit int) int32 {
	if limit <= 0 || limit > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(limit)
}

type FileLatestRepository struct{ store }

func NewFileLatestRepository(pool *pgxpool.Pool) *FileLatestRepository {
	return &FileLatestRepository{store{pool}}
}

func toLatest(f db.FileLatest) domain.FileLatest {
	return domain.FileLatest{
		VaultID:                 f.VaultID,
		FilePath:                f.FilePath,
		CurrentVersion:          int(f.CurrentVersion),
		LatestSnapshotVersion:   int(f.LatestSnapshotVersion),
		Checksum:                f.Checksum,
		SizeBytes:               f.SizeBytes,
		ContentType:             f.ContentType,
		Deleted:                 f.Deleted,
		LastModifiedBy:          f.LastModifiedBy,
		LastDeviceID:            f.LastDeviceID,
		CreatedBy:               f.CreatedBy,
		DeltasSinceSnapshot:     int(f.DeltasSinceSnapshot),
		DeltaBytesSinceSnapshot: f.DeltaBytesSinceSnapshot,
		UpdatedAt:               f.UpdatedAt,
		CreatedAt:               f.CreatedAt,
	}
}

func toLatestList(rows []db.FileLatest) []domain.FileLatest {
	files := make([]domain.FileLatest, len(rows))
	for i, f := range rows {
		files[i] = toLatest(f)
	}
	return files
}

func (r *FileLatestRepository) Upsert(ctx context.Context, f *domain.FileLatest) error {
	createdBy := f.CreatedBy
	if createdBy == uuid.Nil {
		createdBy = f.LastModifiedBy
	}
	return mapError(r.q(ctx).UpsertFileLatest(ctx, db.UpsertFileLatestParams{
		VaultID:                 f.VaultID,
		FilePath:                f.FilePath,
		CurrentVersion:          int32(f.CurrentVersion),
		LatestSnapshotVersion:   int32(f.LatestSnapshotVersion),
		Checksum:                f.Checksum,
		SizeBytes:               f.SizeBytes,
		ContentType:             f.ContentType,
		Deleted:                 f.Deleted,
		LastModifiedBy:          f.LastModifiedBy,
		LastDeviceID:            f.LastDeviceID,
		CreatedBy:               createdBy,
		DeltasSinceSnapshot:     int32(f.DeltasSinceSnapshot),
		DeltaBytesSinceSnapshot: f.DeltaBytesSinceSnapshot,
		UpdatedAt:               f.UpdatedAt,
		CreatedAt:               f.CreatedAt,
	}))
}

func (r *FileLatestRepository) Get(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error) {
	f, err := r.q(ctx).GetFileLatest(ctx, db.GetFileLatestParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	latest := toLatest(f)
	return &latest, nil
}

func (r *FileLatestRepository) GetForUpdate(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error) {
	f, err := r.q(ctx).GetFileLatestForUpdate(ctx, db.GetFileLatestForUpdateParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	latest := toLatest(f)
	return &latest, nil
}

func (r *FileLatestRepository) ListByPaths(ctx context.Context, vaultID uuid.UUID, filePaths []string) ([]domain.FileLatest, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListFileLatestByPaths(ctx, db.ListFileLatestByPathsParams{VaultID: vaultID, Paths: filePaths})
	if err != nil {
		return nil, mapError(err)
	}
	byPath := make(map[string]db.FileLatest, len(rows))
	for _, f := range rows {
		byPath[f.FilePath] = f
	}
	result := make([]domain.FileLatest, 0, len(rows))
	for _, path := range filePaths {
		if f, ok := byPath[path]; ok {
			result = append(result, toLatest(f))
		}
	}
	return result, nil
}

func (r *FileLatestRepository) List(ctx context.Context, query domain.FileListQuery) ([]domain.FileLatest, error) {
	all, exact, patterns := scopeParams(query.Scope)
	params := db.ListFilesParams{
		VaultID:        query.VaultID,
		IncludeDeleted: query.IncludeDeleted,
		AfterPath:      query.AfterPath,
		AllPaths:       all,
		ExactPaths:     exact,
		PathPatterns:   patterns,
		RowLimit:       rowLimit(query.Limit),
	}
	if query.Scope.Unrestricted() {
		rows, err := r.q(ctx).ListFiles(ctx, params)
		if err != nil {
			return nil, mapError(err)
		}
		return toLatestList(rows), nil
	}

	batch := int32(scopedPageSize)
	if query.Limit > 0 && query.Limit < scopedPageSize {
		batch = int32(query.Limit)
	}
	params.RowLimit = batch
	var files []domain.FileLatest
	for {
		rows, err := r.q(ctx).ListFiles(ctx, params)
		if err != nil {
			return nil, mapError(err)
		}
		for _, row := range rows {
			if query.Scope.CanRead(row.FilePath) {
				files = append(files, toLatest(row))
			}
		}
		if query.Limit > 0 && len(files) >= query.Limit {
			return files[:query.Limit], nil
		}
		if len(rows) < int(batch) {
			return files, nil
		}
		params.AfterPath = rows[len(rows)-1].FilePath
	}
}

func (r *FileLatestRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceVersion int) ([]domain.FileLatest, error) {
	files, err := r.List(ctx, domain.FileListQuery{VaultID: vaultID, IncludeDeleted: true, Scope: domain.FullScope()})
	if err != nil || sinceVersion <= 0 {
		return files, err
	}
	filtered := files[:0]
	for _, f := range files {
		if f.CurrentVersion > sinceVersion {
			filtered = append(filtered, f)
		}
	}
	return filtered, nil
}

func (r *FileLatestRepository) Delete(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	return expectRows(r.q(ctx).DeleteFileLatest(ctx, db.DeleteFileLatestParams{VaultID: vaultID, FilePath: filePath}))
}

type FileSnapshotRepository struct{ store }

func NewFileSnapshotRepository(pool *pgxpool.Pool) *FileSnapshotRepository {
	return &FileSnapshotRepository{store{pool}}
}

func toSnapshot(s db.FileSnapshot) domain.FileSnapshot {
	return domain.FileSnapshot{
		ID:               s.ID,
		VaultID:          s.VaultID,
		FilePath:         s.FilePath,
		Version:          int(s.Version),
		EncryptedBlobKey: s.EncryptedBlobKey,
		SizeBytes:        s.SizeBytes,
		Checksum:         s.Checksum,
		CreatedBy:        s.CreatedBy,
		DeviceID:         s.DeviceID,
		CreatedAt:        s.CreatedAt,
	}
}

func toSnapshots(rows []db.FileSnapshot) []domain.FileSnapshot {
	snapshots := make([]domain.FileSnapshot, len(rows))
	for i, s := range rows {
		snapshots[i] = toSnapshot(s)
	}
	return snapshots
}

func (r *FileSnapshotRepository) Create(ctx context.Context, s *domain.FileSnapshot) error {
	return mapError(r.q(ctx).CreateSnapshot(ctx, db.CreateSnapshotParams{
		ID:               s.ID,
		VaultID:          s.VaultID,
		FilePath:         s.FilePath,
		Version:          int32(s.Version),
		EncryptedBlobKey: s.EncryptedBlobKey,
		SizeBytes:        s.SizeBytes,
		Checksum:         s.Checksum,
		CreatedBy:        s.CreatedBy,
		DeviceID:         s.DeviceID,
		CreatedAt:        s.CreatedAt,
	}))
}

func (r *FileSnapshotRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.FileSnapshot, error) {
	s, err := r.q(ctx).GetSnapshot(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	snapshot := toSnapshot(s)
	return &snapshot, nil
}

func (r *FileSnapshotRepository) GetLatest(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileSnapshot, error) {
	s, err := r.q(ctx).GetLatestSnapshot(ctx, db.GetLatestSnapshotParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	snapshot := toSnapshot(s)
	return &snapshot, nil
}

func (r *FileSnapshotRepository) GetByVersion(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error) {
	s, err := r.q(ctx).GetSnapshotByVersion(ctx, db.GetSnapshotByVersionParams{VaultID: vaultID, FilePath: filePath, Version: int32(version)})
	if err != nil {
		return nil, mapError(err)
	}
	snapshot := toSnapshot(s)
	return &snapshot, nil
}

func (r *FileSnapshotRepository) ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error) {
	rows, err := r.q(ctx).ListSnapshotsByPath(ctx, db.ListSnapshotsByPathParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	return toSnapshots(rows), nil
}

func (r *FileSnapshotRepository) ListLatestByPaths(ctx context.Context, vaultID uuid.UUID, paths []string) ([]domain.FileSnapshot, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListLatestSnapshotsByPaths(ctx, db.ListLatestSnapshotsByPathsParams{VaultID: vaultID, Paths: paths})
	if err != nil {
		return nil, mapError(err)
	}
	return toSnapshots(rows), nil
}

func (r *FileSnapshotRepository) ListAtOrBefore(ctx context.Context, vaultID uuid.UUID, paths []string, versions []int32) (map[int]domain.FileSnapshot, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListSnapshotsAtOrBefore(ctx, db.ListSnapshotsAtOrBeforeParams{VaultID: vaultID, Paths: paths, Versions: versions})
	if err != nil {
		return nil, mapError(err)
	}
	pinned := make(map[int]domain.FileSnapshot, len(rows))
	for _, row := range rows {
		pinned[int(row.RequestIndex)] = toSnapshot(db.FileSnapshot{
			ID: row.ID, VaultID: row.VaultID, FilePath: row.FilePath, Version: row.Version,
			EncryptedBlobKey: row.EncryptedBlobKey, SizeBytes: row.SizeBytes, Checksum: row.Checksum,
			CreatedBy: row.CreatedBy, DeviceID: row.DeviceID, CreatedAt: row.CreatedAt,
		})
	}
	return pinned, nil
}

func (r *FileSnapshotRepository) GetAtOrBefore(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error) {
	s, err := r.q(ctx).GetSnapshotAtOrBefore(ctx, db.GetSnapshotAtOrBeforeParams{VaultID: vaultID, FilePath: filePath, Version: int32(version)})
	if err != nil {
		return nil, mapError(err)
	}
	snapshot := toSnapshot(s)
	return &snapshot, nil
}

func (r *FileSnapshotRepository) ListByIDs(ctx context.Context, vaultID uuid.UUID, ids []uuid.UUID) ([]domain.FileSnapshot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListSnapshotsByIDs(ctx, db.ListSnapshotsByIDsParams{VaultID: vaultID, Ids: ids})
	if err != nil {
		return nil, mapError(err)
	}
	return toSnapshots(rows), nil
}

func (r *FileSnapshotRepository) ListHistory(ctx context.Context, vaultID uuid.UUID, filePath string, limit int) ([]domain.HistoryEntry, error) {
	rows, err := r.q(ctx).ListHistory(ctx, db.ListHistoryParams{VaultID: vaultID, FilePath: filePath, RowLimit: rowLimit(limit)})
	if err != nil {
		return nil, mapError(err)
	}
	entries := make([]domain.HistoryEntry, len(rows))
	for i, h := range rows {
		entries[i] = domain.HistoryEntry{
			FileSnapshot: toSnapshot(db.FileSnapshot{
				ID: h.ID, VaultID: h.VaultID, FilePath: h.FilePath, Version: h.Version,
				EncryptedBlobKey: h.EncryptedBlobKey, SizeBytes: h.SizeBytes, Checksum: h.Checksum,
				CreatedBy: h.CreatedBy, DeviceID: h.DeviceID, CreatedAt: h.CreatedAt,
			}),
			AuthorName:      h.AuthorName,
			AuthorAvatarKey: h.AuthorAvatarKey,
			DeviceName:      h.DeviceName,
		}
	}
	return entries, nil
}

func (r *FileSnapshotRepository) DeleteOlderVersions(ctx context.Context, vaultID uuid.UUID, filePath string, keepCount int) ([]domain.FileSnapshot, error) {
	rows, err := r.q(ctx).DeleteOlderSnapshots(ctx, db.DeleteOlderSnapshotsParams{VaultID: vaultID, FilePath: filePath, KeepCount: int32(keepCount)})
	if err != nil {
		return nil, mapError(err)
	}
	return toSnapshots(rows), nil
}

func (r *FileSnapshotRepository) DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error) {
	rows, err := r.q(ctx).DeleteSnapshotsByPath(ctx, db.DeleteSnapshotsByPathParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	return toSnapshots(rows), nil
}

func (r *FileSnapshotRepository) MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	return mapError(r.q(ctx).MoveSnapshots(ctx, db.MoveSnapshotsParams{VaultID: vaultID, OldPath: oldPath, NewPath: newPath}))
}

func (r *FileSnapshotRepository) ListBlobKeysByVault(ctx context.Context, vaultID uuid.UUID) ([]string, error) {
	keys, err := r.q(ctx).ListBlobKeysByVault(ctx, vaultID)
	return keys, mapError(err)
}

type FileDeltaRepository struct{ store }

func NewFileDeltaRepository(pool *pgxpool.Pool) *FileDeltaRepository {
	return &FileDeltaRepository{store{pool}}
}

func (r *FileDeltaRepository) Create(ctx context.Context, d *domain.FileDelta) error {
	return mapError(r.q(ctx).CreateDelta(ctx, db.CreateDeltaParams{
		ID:             d.ID,
		VaultID:        d.VaultID,
		FilePath:       d.FilePath,
		BaseVersion:    int32(d.BaseVersion),
		TargetVersion:  int32(d.TargetVersion),
		EncryptedDelta: d.EncryptedDelta,
		SizeBytes:      d.SizeBytes,
		CreatedBy:      d.CreatedBy,
		DeviceID:       d.DeviceID,
		CreatedAt:      d.CreatedAt,
	}))
}

func (r *FileDeltaRepository) ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, sinceVersion int) ([]domain.FileDelta, error) {
	rows, err := r.q(ctx).ListDeltas(ctx, db.ListDeltasParams{VaultID: vaultID, FilePath: filePath, SinceVersion: int32(sinceVersion)})
	if err != nil {
		return nil, mapError(err)
	}
	deltas := make([]domain.FileDelta, len(rows))
	for i, d := range rows {
		deltas[i] = domain.FileDelta{
			ID: d.ID, VaultID: d.VaultID, FilePath: d.FilePath, BaseVersion: int(d.BaseVersion),
			TargetVersion: int(d.TargetVersion), EncryptedDelta: d.EncryptedDelta, SizeBytes: d.SizeBytes,
			CreatedBy: d.CreatedBy, DeviceID: d.DeviceID, CreatedAt: d.CreatedAt,
		}
	}
	return deltas, nil
}

func (r *FileDeltaRepository) DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, beforeVersion int) (int64, error) {
	if beforeVersion > math.MaxInt32 {
		beforeVersion = math.MaxInt32
	}
	n, err := r.q(ctx).DeleteDeltasBefore(ctx, db.DeleteDeltasBeforeParams{VaultID: vaultID, FilePath: filePath, BeforeVersion: int32(beforeVersion)})
	return n, mapError(err)
}

func (r *FileDeltaRepository) MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	return mapError(r.q(ctx).MoveDeltas(ctx, db.MoveDeltasParams{VaultID: vaultID, OldPath: oldPath, NewPath: newPath}))
}

type SyncEventRepository struct{ store }

func NewSyncEventRepository(pool *pgxpool.Pool) *SyncEventRepository {
	return &SyncEventRepository{store{pool}}
}

func toEvent(e db.SyncEvent) domain.SyncEvent {
	event := domain.SyncEvent{
		ID:        e.ID,
		VaultID:   e.VaultID,
		EventType: domain.EventType(e.EventType),
		FilePath:  e.FilePath,
		Version:   int(e.Version),
		ActorID:   e.ActorID,
		DeviceID:  e.DeviceID,
		CreatedAt: e.CreatedAt,
	}
	if len(e.Metadata) > 0 {
		_ = json.Unmarshal(e.Metadata, &event.Metadata)
	}
	return event
}

func (r *SyncEventRepository) Create(ctx context.Context, event *domain.SyncEvent) error {
	q := r.q(ctx)
	if err := q.LockVault(ctx, event.VaultID); err != nil {
		return mapError(err)
	}
	metadata := event.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	id, err := q.CreateSyncEvent(ctx, db.CreateSyncEventParams{
		VaultID:   event.VaultID,
		EventType: string(event.EventType),
		FilePath:  event.FilePath,
		Version:   int32(event.Version),
		ActorID:   event.ActorID,
		DeviceID:  event.DeviceID,
		Metadata:  payload,
		CreatedAt: event.CreatedAt,
	})
	if err != nil {
		return mapError(err)
	}
	event.ID = id
	return nil
}

func (r *SyncEventRepository) GetByID(ctx context.Context, id int64) (*domain.SyncEvent, error) {
	e, err := r.q(ctx).GetSyncEvent(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	event := toEvent(e)
	return &event, nil
}

func (r *SyncEventRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error) {
	return r.ListScoped(ctx, vaultID, sinceID, limit, domain.FullScope())
}

func (r *SyncEventRepository) ListScoped(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int, scope domain.Scope) ([]domain.SyncEvent, error) {
	all, exact, patterns := scopeParams(scope)
	params := db.ListSyncEventsParams{
		VaultID: vaultID, SinceID: sinceID, AllPaths: all, ExactPaths: exact, PathPatterns: patterns, RowLimit: rowLimit(limit),
	}
	if scope.Unrestricted() {
		rows, err := r.q(ctx).ListSyncEvents(ctx, params)
		if err != nil {
			return nil, mapError(err)
		}
		events := make([]domain.SyncEvent, len(rows))
		for i, e := range rows {
			events[i] = toEvent(e)
		}
		return events, nil
	}

	batch := int32(scopedPageSize)
	if limit > 0 && limit < scopedPageSize {
		batch = int32(limit)
	}
	params.RowLimit = batch
	var events []domain.SyncEvent
	for {
		rows, err := r.q(ctx).ListSyncEvents(ctx, params)
		if err != nil {
			return nil, mapError(err)
		}
		for _, row := range rows {
			if event := toEvent(row); eventVisible(scope, event) {
				events = append(events, event)
			}
		}
		if limit > 0 && len(events) >= limit {
			return events[:limit], nil
		}
		if len(rows) < int(batch) {
			return events, nil
		}
		params.SinceID = rows[len(rows)-1].ID
	}
}

func (r *SyncEventRepository) LatestID(ctx context.Context, vaultID uuid.UUID) (int64, error) {
	id, err := r.q(ctx).LatestSyncEventID(ctx, vaultID)
	return id, mapError(err)
}

func (r *SyncEventRepository) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	n, err := r.q(ctx).DeleteSyncEventsBefore(ctx, before)
	return n, mapError(err)
}
