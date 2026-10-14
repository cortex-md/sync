package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type CollabDocumentRepository struct {
	mu        sync.RWMutex
	updates   []*domain.CollabUpdate
	documents map[string]*domain.CollabDocument
	nextID    int64
}

func NewCollabDocumentRepository() *CollabDocumentRepository {
	return &CollabDocumentRepository{
		documents: make(map[string]*domain.CollabDocument),
		nextID:    1,
	}
}

func collabDocKey(vaultID uuid.UUID, filePath string) string {
	return vaultID.String() + ":" + filePath
}

func (r *CollabDocumentRepository) BatchStoreUpdates(_ context.Context, vaultID uuid.UUID, filePath string, updates [][]byte) error {
	if len(updates) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := collabDocKey(vaultID, filePath)
	doc, exists := r.documents[key]
	if !exists {
		doc = &domain.CollabDocument{
			VaultID:  vaultID,
			FilePath: filePath,
		}
		r.documents[key] = doc
	}

	for _, data := range updates {
		cp := make([]byte, len(data))
		copy(cp, data)
		stored := &domain.CollabUpdate{
			ID:        r.nextID,
			VaultID:   vaultID,
			FilePath:  filePath,
			Data:      cp,
			CreatedAt: time.Now(),
		}
		r.nextID++
		r.updates = append(r.updates, stored)
		doc.UpdateCount++
	}
	doc.UpdatedAt = time.Now()
	return nil
}

func (r *CollabDocumentRepository) StoreUpdate(_ context.Context, update *domain.CollabUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *update
	stored.ID = r.nextID
	r.nextID++
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = time.Now()
	}
	r.updates = append(r.updates, &stored)

	key := collabDocKey(update.VaultID, update.FilePath)
	doc, exists := r.documents[key]
	if !exists {
		doc = &domain.CollabDocument{
			VaultID:  update.VaultID,
			FilePath: update.FilePath,
		}
		r.documents[key] = doc
	}
	doc.UpdateCount++
	doc.UpdatedAt = time.Now()
	return nil
}

func (r *CollabDocumentRepository) LoadDocument(_ context.Context, vaultID uuid.UUID, filePath string) (*domain.CollabDocument, []domain.CollabUpdate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := collabDocKey(vaultID, filePath)
	doc, exists := r.documents[key]
	if !exists {
		return nil, nil, nil
	}
	docCopy := *doc

	var incremental []domain.CollabUpdate
	for _, u := range r.updates {
		if u.VaultID == vaultID && u.FilePath == filePath {
			incremental = append(incremental, *u)
		}
	}
	return &docCopy, incremental, nil
}

func (r *CollabDocumentRepository) CompactDocument(_ context.Context, vaultID uuid.UUID, filePath string, compactedState []byte, upToUpdateID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := collabDocKey(vaultID, filePath)
	doc, exists := r.documents[key]
	if !exists {
		doc = &domain.CollabDocument{
			VaultID:  vaultID,
			FilePath: filePath,
		}
		r.documents[key] = doc
	}

	stateCopy := make([]byte, len(compactedState))
	copy(stateCopy, compactedState)

	doc.CompactedState = stateCopy
	doc.StateVector = nil
	doc.UpdatedAt = time.Now()

	remaining := r.updates[:0]
	removed := 0
	for _, u := range r.updates {
		if u.VaultID == vaultID && u.FilePath == filePath && u.ID <= upToUpdateID {
			removed++
			continue
		}
		remaining = append(remaining, u)
	}
	r.updates = remaining
	doc.UpdateCount = max(doc.UpdateCount-removed, 0)
	return nil
}

func (r *CollabDocumentRepository) MovePath(_ context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.documents, collabDocKey(vaultID, newPath))
	remaining := r.updates[:0]
	for _, u := range r.updates {
		if u.VaultID == vaultID && u.FilePath == newPath {
			continue
		}
		if u.VaultID == vaultID && u.FilePath == oldPath {
			u.FilePath = newPath
		}
		remaining = append(remaining, u)
	}
	r.updates = remaining

	if doc, ok := r.documents[collabDocKey(vaultID, oldPath)]; ok {
		delete(r.documents, collabDocKey(vaultID, oldPath))
		doc.FilePath = newPath
		r.documents[collabDocKey(vaultID, newPath)] = doc
	}
	return nil
}

func (r *CollabDocumentRepository) DeleteForPath(_ context.Context, vaultID uuid.UUID, filePath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.documents, collabDocKey(vaultID, filePath))

	remaining := r.updates[:0]
	for _, u := range r.updates {
		if !(u.VaultID == vaultID && u.FilePath == filePath) {
			remaining = append(remaining, u)
		}
	}
	r.updates = remaining
	return nil
}
