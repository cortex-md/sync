package fake_test

import (
	"context"
	"sync"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollabDocumentRepository_StoreUpdate(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	update := &domain.CollabUpdate{
		VaultID:  vaultID,
		FilePath: "notes/hello.md",
		Data:     []byte{0x01, 0x02, 0x03},
	}
	require.NoError(t, repo.StoreUpdate(ctx, update))

	doc, updates, err := repo.LoadDocument(ctx, vaultID, "notes/hello.md")
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Equal(t, 1, doc.UpdateCount)
	require.Len(t, updates, 1)
	assert.Equal(t, update.Data, updates[0].Data)
	assert.NotZero(t, updates[0].ID)
}

func TestCollabDocumentRepository_StoreUpdate_AssignsSequentialIDs(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	for i := 0; i < 3; i++ {
		require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
			VaultID:  vaultID,
			FilePath: "a.md",
			Data:     []byte{byte(i)},
		}))
	}

	_, updates, err := repo.LoadDocument(ctx, vaultID, "a.md")
	require.NoError(t, err)
	assert.Equal(t, int64(1), updates[0].ID)
	assert.Equal(t, int64(2), updates[1].ID)
	assert.Equal(t, int64(3), updates[2].ID)
}

func TestCollabDocumentRepository_LoadDocument_NonExistent(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()

	doc, updates, err := repo.LoadDocument(ctx, uuid.New(), "missing.md")
	require.NoError(t, err)
	assert.Nil(t, doc)
	assert.Empty(t, updates)
}

func TestCollabDocumentRepository_LoadDocument_IsolatedByVault(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vault1 := uuid.New()
	vault2 := uuid.New()

	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vault1,
		FilePath: "shared.md",
		Data:     []byte{0x01},
	}))
	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vault2,
		FilePath: "shared.md",
		Data:     []byte{0x02},
	}))

	_, updates1, err := repo.LoadDocument(ctx, vault1, "shared.md")
	require.NoError(t, err)
	require.Len(t, updates1, 1)
	assert.Equal(t, []byte{0x01}, updates1[0].Data)

	_, updates2, err := repo.LoadDocument(ctx, vault2, "shared.md")
	require.NoError(t, err)
	require.Len(t, updates2, 1)
	assert.Equal(t, []byte{0x02}, updates2[0].Data)
}

func TestCollabDocumentRepository_CompactDocument(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	for i := 0; i < 5; i++ {
		require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
			VaultID:  vaultID,
			FilePath: "doc.md",
			Data:     []byte{byte(i)},
		}))
	}

	compacted := []byte("full-state")
	sv := []byte("state-vector")
	require.NoError(t, repo.CompactDocument(ctx, vaultID, "doc.md", compacted, sv))

	doc, updates, err := repo.LoadDocument(ctx, vaultID, "doc.md")
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Equal(t, compacted, doc.CompactedState)
	assert.Equal(t, sv, doc.StateVector)
	assert.Equal(t, 0, doc.UpdateCount)
	assert.Empty(t, updates)
}

func TestCollabDocumentRepository_CompactDocument_DoesNotAffectOtherFiles(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vaultID,
		FilePath: "a.md",
		Data:     []byte{0x01},
	}))
	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vaultID,
		FilePath: "b.md",
		Data:     []byte{0x02},
	}))

	require.NoError(t, repo.CompactDocument(ctx, vaultID, "a.md", []byte("compact"), []byte("sv")))

	_, updatesA, err := repo.LoadDocument(ctx, vaultID, "a.md")
	require.NoError(t, err)
	assert.Empty(t, updatesA)

	_, updatesB, err := repo.LoadDocument(ctx, vaultID, "b.md")
	require.NoError(t, err)
	assert.Len(t, updatesB, 1)
}

func TestCollabDocumentRepository_DeleteDocument(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vaultID,
		FilePath: "del.md",
		Data:     []byte{0x01},
	}))

	require.NoError(t, repo.DeleteDocument(ctx, vaultID, "del.md"))

	doc, updates, err := repo.LoadDocument(ctx, vaultID, "del.md")
	require.NoError(t, err)
	assert.Nil(t, doc)
	assert.Empty(t, updates)
}

func TestCollabDocumentRepository_DeleteDocument_NotFound(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	err := repo.DeleteDocument(context.Background(), uuid.New(), "ghost.md")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCollabDocumentRepository_DeleteDocument_DoesNotAffectOtherFiles(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vaultID,
		FilePath: "keep.md",
		Data:     []byte{0x01},
	}))
	require.NoError(t, repo.StoreUpdate(ctx, &domain.CollabUpdate{
		VaultID:  vaultID,
		FilePath: "remove.md",
		Data:     []byte{0x02},
	}))

	require.NoError(t, repo.DeleteDocument(ctx, vaultID, "remove.md"))

	doc, updates, err := repo.LoadDocument(ctx, vaultID, "keep.md")
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Len(t, updates, 1)
}

func TestCollabDocumentRepository_ConcurrentStoreUpdate(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	var wg sync.WaitGroup
	n := 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = repo.StoreUpdate(ctx, &domain.CollabUpdate{
				VaultID:  vaultID,
				FilePath: "concurrent.md",
				Data:     []byte{byte(i)},
			})
		}(i)
	}
	wg.Wait()

	doc, updates, err := repo.LoadDocument(ctx, vaultID, "concurrent.md")
	require.NoError(t, err)
	assert.Equal(t, n, doc.UpdateCount)
	assert.Len(t, updates, n)
}

func TestCollabDocumentRepository_CompactDocument_RetainsMutationIsolation(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	original := []byte("state")
	sv := []byte("sv")
	require.NoError(t, repo.CompactDocument(ctx, vaultID, "x.md", original, sv))

	original[0] = 'Z'

	doc, _, err := repo.LoadDocument(ctx, vaultID, "x.md")
	require.NoError(t, err)
	assert.Equal(t, byte('s'), doc.CompactedState[0])
}

func TestCollabDocumentRepository_BatchStoreUpdates(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	updates := [][]byte{{0x01}, {0x02}, {0x03}}
	require.NoError(t, repo.BatchStoreUpdates(ctx, vaultID, "batch.md", updates))

	doc, stored, err := repo.LoadDocument(ctx, vaultID, "batch.md")
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Equal(t, 3, doc.UpdateCount)
	require.Len(t, stored, 3)
	assert.Equal(t, []byte{0x01}, stored[0].Data)
	assert.Equal(t, []byte{0x02}, stored[1].Data)
	assert.Equal(t, []byte{0x03}, stored[2].Data)
}

func TestCollabDocumentRepository_BatchStoreUpdates_EmptyIsNoOp(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	require.NoError(t, repo.BatchStoreUpdates(ctx, vaultID, "empty.md", nil))
	require.NoError(t, repo.BatchStoreUpdates(ctx, vaultID, "empty.md", [][]byte{}))

	doc, updates, err := repo.LoadDocument(ctx, vaultID, "empty.md")
	require.NoError(t, err)
	assert.Nil(t, doc)
	assert.Empty(t, updates)
}

func TestCollabDocumentRepository_BatchStoreUpdates_CopiesData(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	original := []byte{0x01, 0x02}
	require.NoError(t, repo.BatchStoreUpdates(ctx, vaultID, "copy.md", [][]byte{original}))
	original[0] = 0xFF

	_, stored, err := repo.LoadDocument(ctx, vaultID, "copy.md")
	require.NoError(t, err)
	assert.Equal(t, byte(0x01), stored[0].Data[0], "stored data should be a copy")
}

func TestCollabDocumentRepository_BatchStoreUpdates_AccumulatesAcrossCalls(t *testing.T) {
	repo := fake.NewCollabDocumentRepository()
	ctx := context.Background()
	vaultID := uuid.New()

	require.NoError(t, repo.BatchStoreUpdates(ctx, vaultID, "acc.md", [][]byte{{0x01}, {0x02}}))
	require.NoError(t, repo.BatchStoreUpdates(ctx, vaultID, "acc.md", [][]byte{{0x03}}))

	doc, stored, err := repo.LoadDocument(ctx, vaultID, "acc.md")
	require.NoError(t, err)
	assert.Equal(t, 3, doc.UpdateCount)
	assert.Len(t, stored, 3)
}
