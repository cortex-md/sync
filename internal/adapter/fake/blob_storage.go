package fake

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/domain"
)

type BlobStorage struct {
	mu    sync.RWMutex
	blobs map[string][]byte
}

func NewBlobStorage() *BlobStorage {
	return &BlobStorage{
		blobs: make(map[string][]byte),
	}
}

func (s *BlobStorage) Upload(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[key] = data
	return nil
}

func (s *BlobStorage) Download(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, exists := s.blobs[key]
	if !exists {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *BlobStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blobs, key)
	return nil
}

func (s *BlobStorage) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.blobs[key]
	return exists, nil
}

func (s *BlobStorage) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.blobs)
}
