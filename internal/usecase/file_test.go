package usecase_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fileTestDeps struct {
	snapshots *fake.FileSnapshotRepository
	deltas    *fake.FileDeltaRepository
	latest    *fake.FileLatestRepository
	events    *fake.SyncEventRepository
	members   *fake.VaultMemberRepository
	users     *fake.UserRepository
	devices   *fake.DeviceRepository
	blobs     *fake.BlobStorage
	uc        *usecase.FileUsecase
}

func setupFileTest() *fileTestDeps {
	snapshots := fake.NewFileSnapshotRepository()
	deltas := fake.NewFileDeltaRepository()
	latest := fake.NewFileLatestRepository()
	events := fake.NewSyncEventRepository()
	members := fake.NewVaultMemberRepository()
	users := fake.NewUserRepository()
	devices := fake.NewDeviceRepository()
	blobs := fake.NewBlobStorage()

	uc := usecase.NewFileUsecase(snapshots, deltas, latest, events, members, users, devices, blobs, fake.NewTransactor())

	return &fileTestDeps{
		snapshots: snapshots,
		deltas:    deltas,
		latest:    latest,
		events:    events,
		members:   members,
		users:     users,
		devices:   devices,
		blobs:     blobs,
		uc:        uc,
	}
}

func addTestMember(t *testing.T, deps *fileTestDeps, vaultID, userID uuid.UUID, role domain.VaultRole) {
	t.Helper()
	err := deps.members.Add(context.Background(), &domain.VaultMember{
		VaultID:  vaultID,
		UserID:   userID,
		Role:     role,
		JoinedAt: time.Now(),
	})
	require.NoError(t, err)
}

func contentChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

func uploadTestSnapshot(t *testing.T, deps *fileTestDeps, userID, deviceID, vaultID uuid.UUID, filePath string, content []byte) *usecase.FileInfo {
	t.Helper()
	info, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    filePath,
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	require.NoError(t, err)
	return info
}

func TestFileUsecase_UploadSnapshot_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("# Hello World")
	info := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", content)

	assert.Equal(t, vaultID, info.VaultID)
	assert.Equal(t, "notes/hello.md", info.FilePath)
	assert.Equal(t, 1, info.Version)
	assert.Equal(t, "text/markdown", info.ContentType)
	assert.False(t, info.Deleted)
	assert.Equal(t, userID, info.LastModifiedBy)
	assert.Equal(t, deviceID, info.LastDeviceID)
	assert.NotZero(t, info.SnapshotID)
}

func TestFileUsecase_UploadSnapshot_VersionIncrement(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	info1 := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))
	assert.Equal(t, 1, info1.Version)

	info2 := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v2"))
	assert.Equal(t, 2, info2.Version)

	info3 := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v3"))
	assert.Equal(t, 3, info3.Version)
}

func TestFileUsecase_UploadSnapshot_EmptyPath(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	_, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "",
		Data:     bytes.NewReader([]byte("test")),
	})
	assert.Equal(t, domain.ErrInvalidInput, err)
}

func TestFileUsecase_UploadSnapshot_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()

	_, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "test.md",
		Data:     bytes.NewReader([]byte("test")),
	})
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_UploadSnapshot_ViewerCantWrite(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleViewer)

	_, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "test.md",
		Data:     bytes.NewReader([]byte("test")),
	})
	assert.Equal(t, domain.ErrInsufficientRole, err)
}

func TestFileUsecase_UploadSnapshot_CreatesEvent(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	events, err := deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, domain.EventFileCreated, events[0].EventType)
	assert.Equal(t, "notes/hello.md", events[0].FilePath)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v2"))

	events, err = deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, domain.EventFileUpdated, events[1].EventType)
}

func TestFileUsecase_UploadSnapshot_StoresBlob(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("# My Important Note")
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/important.md", content)

	result, err := deps.uc.DownloadSnapshot(context.Background(), userID, vaultID, "notes/important.md", 0)
	require.NoError(t, err)
	defer result.Reader.Close()

	downloaded, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, content, downloaded)
}

func TestFileUsecase_UploadDelta_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	deltaData := []byte("encrypted-delta-content")
	info, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   1,
		Checksum:      "delta-checksum",
		SizeBytes:     100,
		EncryptedData: deltaData,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, info.Version)
	assert.Equal(t, "delta-checksum", info.Checksum)
}

func TestFileUsecase_UploadDelta_ConflictVersion(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   99,
		Checksum:      "checksum",
		SizeBytes:     50,
		EncryptedData: []byte("delta"),
	})
	assert.Equal(t, domain.ErrConflict, err)
}

func TestFileUsecase_UploadDelta_NoExistingFile(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/nonexistent.md",
		BaseVersion:   1,
		Checksum:      "checksum",
		SizeBytes:     50,
		EncryptedData: []byte("delta"),
	})
	assert.Equal(t, domain.ErrNotFound, err)
}

func TestFileUsecase_UploadDelta_EmptyData(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   1,
		Checksum:      "checksum",
		SizeBytes:     0,
		EncryptedData: nil,
	})
	assert.Equal(t, domain.ErrInvalidInput, err)
}

func TestFileUsecase_UploadDelta_ViewerCantWrite(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleViewer)

	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   1,
		Checksum:      "checksum",
		SizeBytes:     50,
		EncryptedData: []byte("delta"),
	})
	assert.Equal(t, domain.ErrInsufficientRole, err)
}

func TestFileUsecase_UploadDelta_CreatesEvent(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   1,
		Checksum:      "checksum",
		SizeBytes:     50,
		EncryptedData: []byte("delta"),
	})
	require.NoError(t, err)

	events, err := deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, domain.EventFileUpdated, events[1].EventType)
}

func TestFileUsecase_DownloadSnapshot_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("# Test Content")
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", content)

	result, err := deps.uc.DownloadSnapshot(context.Background(), userID, vaultID, "notes/test.md", 0)
	require.NoError(t, err)
	defer result.Reader.Close()

	assert.Equal(t, 1, result.Snapshot.Version)
	downloaded, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, content, downloaded)
}

func TestFileUsecase_DownloadSnapshot_SpecificVersion(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("v1-content"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("v2-content"))

	result, err := deps.uc.DownloadSnapshot(context.Background(), userID, vaultID, "notes/test.md", 1)
	require.NoError(t, err)
	defer result.Reader.Close()

	assert.Equal(t, 1, result.Snapshot.Version)
	downloaded, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1-content"), downloaded)
}

func TestFileUsecase_DownloadSnapshot_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()

	_, err := deps.uc.DownloadSnapshot(context.Background(), userID, vaultID, "notes/test.md", 0)
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_DownloadSnapshot_NotFound(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleViewer)

	_, err := deps.uc.DownloadSnapshot(context.Background(), userID, vaultID, "notes/nonexistent.md", 0)
	assert.Equal(t, domain.ErrNotFound, err)
}

func TestFileUsecase_DownloadSnapshot_ViewerCanRead(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	ownerID := uuid.New()
	viewerID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, ownerID, domain.VaultRoleOwner)
	addTestMember(t, deps, vaultID, viewerID, domain.VaultRoleViewer)

	uploadTestSnapshot(t, deps, ownerID, deviceID, vaultID, "notes/test.md", []byte("content"))

	result, err := deps.uc.DownloadSnapshot(context.Background(), viewerID, vaultID, "notes/test.md", 0)
	require.NoError(t, err)
	defer result.Reader.Close()

	downloaded, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, []byte("content"), downloaded)
}

func TestFileUsecase_DownloadDeltas_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   1,
		Checksum:      "c1",
		SizeBytes:     50,
		EncryptedData: []byte("delta-1"),
	})
	require.NoError(t, err)

	result, err := deps.uc.DownloadDeltas(context.Background(), userID, vaultID, "notes/hello.md", 1)
	require.NoError(t, err)
	assert.Len(t, result.Deltas, 1)
	assert.Equal(t, 1, result.Deltas[0].BaseVersion)
	assert.Equal(t, 2, result.Deltas[0].TargetVersion)
}

func TestFileUsecase_DownloadDeltas_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()

	_, err := deps.uc.DownloadDeltas(context.Background(), userID, vaultID, "test.md", 0)
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_DeleteFile_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("content"))

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "notes/hello.md",
	})
	require.NoError(t, err)

	info, err := deps.uc.GetFileInfo(context.Background(), userID, vaultID, "notes/hello.md")
	require.NoError(t, err)
	assert.True(t, info.Deleted)
}

func TestFileUsecase_DeleteFile_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "notes/hello.md",
	})
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_DeleteFile_ViewerCantDelete(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	ownerID := uuid.New()
	viewerID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, ownerID, domain.VaultRoleOwner)
	addTestMember(t, deps, vaultID, viewerID, domain.VaultRoleViewer)

	uploadTestSnapshot(t, deps, ownerID, deviceID, vaultID, "notes/hello.md", []byte("content"))

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID:   viewerID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "notes/hello.md",
	})
	assert.Equal(t, domain.ErrInsufficientRole, err)
}

func TestFileUsecase_DeleteFile_NotFound(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		FilePath: "notes/nonexistent.md",
	})
	assert.Equal(t, domain.ErrNotFound, err)
}

func TestFileUsecase_DeleteFile_AlreadyDeleted(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("content"))

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID: userID, DeviceID: deviceID, VaultID: vaultID, FilePath: "notes/hello.md",
	})
	require.NoError(t, err)

	err = deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID: userID, DeviceID: deviceID, VaultID: vaultID, FilePath: "notes/hello.md",
	})
	assert.Equal(t, domain.ErrNotFound, err)
}

func TestFileUsecase_DeleteFile_CreatesEvent(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("content"))

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID: userID, DeviceID: deviceID, VaultID: vaultID, FilePath: "notes/hello.md",
	})
	require.NoError(t, err)

	events, err := deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, domain.EventFileDeleted, events[1].EventType)
}

func TestFileUsecase_RenameFile_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/old-name.md", []byte("content"))

	info, err := deps.uc.RenameFile(context.Background(), usecase.RenameFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  "notes/old-name.md",
		NewPath:  "notes/new-name.md",
	})
	require.NoError(t, err)
	assert.Equal(t, "notes/new-name.md", info.FilePath)
	assert.False(t, info.Deleted)

	oldInfo, err := deps.uc.GetFileInfo(context.Background(), userID, vaultID, "notes/old-name.md")
	require.NoError(t, err)
	assert.True(t, oldInfo.Deleted)
}

func TestFileUsecase_RenameFile_SamePath(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	_, err := deps.uc.RenameFile(context.Background(), usecase.RenameFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  "notes/same.md",
		NewPath:  "notes/same.md",
	})
	assert.Equal(t, domain.ErrInvalidInput, err)
}

func TestFileUsecase_RenameFile_TargetExists(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b"))

	_, err := deps.uc.RenameFile(context.Background(), usecase.RenameFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  "notes/a.md",
		NewPath:  "notes/b.md",
	})
	assert.Equal(t, domain.ErrAlreadyExists, err)
}

func TestFileUsecase_RenameFile_SourceNotFound(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	_, err := deps.uc.RenameFile(context.Background(), usecase.RenameFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  "notes/nonexistent.md",
		NewPath:  "notes/new.md",
	})
	assert.Equal(t, domain.ErrNotFound, err)
}

func TestFileUsecase_RenameFile_ViewerCantRename(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	ownerID := uuid.New()
	viewerID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, ownerID, domain.VaultRoleOwner)
	addTestMember(t, deps, vaultID, viewerID, domain.VaultRoleViewer)

	uploadTestSnapshot(t, deps, ownerID, deviceID, vaultID, "notes/hello.md", []byte("content"))

	_, err := deps.uc.RenameFile(context.Background(), usecase.RenameFileInput{
		UserID:   viewerID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  "notes/hello.md",
		NewPath:  "notes/renamed.md",
	})
	assert.Equal(t, domain.ErrInsufficientRole, err)
}

func TestFileUsecase_RenameFile_CreatesEvent(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/old.md", []byte("content"))

	_, err := deps.uc.RenameFile(context.Background(), usecase.RenameFileInput{
		UserID:   userID,
		DeviceID: deviceID,
		VaultID:  vaultID,
		OldPath:  "notes/old.md",
		NewPath:  "notes/new.md",
	})
	require.NoError(t, err)

	events, err := deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, domain.EventFileRenamed, events[1].EventType)
	assert.Equal(t, "notes/new.md", events[1].FilePath)
	assert.Equal(t, "notes/old.md", events[1].Metadata["old_path"])
}

func TestFileUsecase_GetHistory_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v2"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v3"))

	history, err := deps.uc.GetHistory(context.Background(), userID, vaultID, "notes/hello.md")
	require.NoError(t, err)
	assert.Len(t, history, 3)
	assert.Equal(t, 1, history[0].Version)
	assert.Equal(t, 2, history[1].Version)
	assert.Equal(t, 3, history[2].Version)
}

func TestFileUsecase_GetHistory_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()

	_, err := deps.uc.GetHistory(context.Background(), userID, vaultID, "notes/hello.md")
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_GetHistory_Empty(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	history, err := deps.uc.GetHistory(context.Background(), userID, vaultID, "notes/nonexistent.md")
	require.NoError(t, err)
	assert.Len(t, history, 0)
}

func TestFileUsecase_ListFiles_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/c.md", []byte("c"))

	files, err := deps.uc.ListFiles(context.Background(), userID, vaultID, false)
	require.NoError(t, err)
	assert.Len(t, files, 3)
}

func TestFileUsecase_ListFiles_ExcludesDeleted(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b"))

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID: userID, DeviceID: deviceID, VaultID: vaultID, FilePath: "notes/b.md",
	})
	require.NoError(t, err)

	files, err := deps.uc.ListFiles(context.Background(), userID, vaultID, false)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, "notes/a.md", files[0].FilePath)
}

func TestFileUsecase_ListFiles_IncludesDeleted(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b"))

	err := deps.uc.DeleteFile(context.Background(), usecase.DeleteFileInput{
		UserID: userID, DeviceID: deviceID, VaultID: vaultID, FilePath: "notes/b.md",
	})
	require.NoError(t, err)

	files, err := deps.uc.ListFiles(context.Background(), userID, vaultID, true)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestFileUsecase_ListFiles_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()

	_, err := deps.uc.ListFiles(context.Background(), userID, vaultID, false)
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_GetFileInfo_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("content"))

	info, err := deps.uc.GetFileInfo(context.Background(), userID, vaultID, "notes/hello.md")
	require.NoError(t, err)
	assert.Equal(t, "notes/hello.md", info.FilePath)
	assert.Equal(t, 1, info.Version)
	assert.False(t, info.Deleted)
}

func TestFileUsecase_GetFileInfo_NotFound(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	_, err := deps.uc.GetFileInfo(context.Background(), userID, vaultID, "notes/nonexistent.md")
	assert.Equal(t, domain.ErrNotFound, err)
}

func TestFileUsecase_ListChanges_Success(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b"))

	result, err := deps.uc.ListChanges(context.Background(), userID, vaultID, 0, 100)
	require.NoError(t, err)
	assert.Len(t, result.Events, 2)
}

func TestFileUsecase_ListChanges_SinceID(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b"))

	result, err := deps.uc.ListChanges(context.Background(), userID, vaultID, 1, 100)
	require.NoError(t, err)
	assert.Len(t, result.Events, 1)
	assert.Equal(t, "notes/b.md", result.Events[0].FilePath)
}

func TestFileUsecase_ListChanges_DefaultLimit(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	result, err := deps.uc.ListChanges(context.Background(), userID, vaultID, 0, 0)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestFileUsecase_ListChanges_NotMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()

	_, err := deps.uc.ListChanges(context.Background(), userID, vaultID, 0, 100)
	assert.Equal(t, domain.ErrVaultAccessDenied, err)
}

func TestFileUsecase_MultiUserAttribution(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	user1 := uuid.New()
	user2 := uuid.New()
	device1 := uuid.New()
	device2 := uuid.New()
	addTestMember(t, deps, vaultID, user1, domain.VaultRoleOwner)
	addTestMember(t, deps, vaultID, user2, domain.VaultRoleEditor)

	info1 := uploadTestSnapshot(t, deps, user1, device1, vaultID, "notes/shared.md", []byte("v1-by-user1"))
	assert.Equal(t, user1, info1.LastModifiedBy)
	assert.Equal(t, device1, info1.LastDeviceID)

	info2 := uploadTestSnapshot(t, deps, user2, device2, vaultID, "notes/shared.md", []byte("v2-by-user2"))
	assert.Equal(t, user2, info2.LastModifiedBy)
	assert.Equal(t, device2, info2.LastDeviceID)
	assert.Equal(t, 2, info2.Version)

	history, err := deps.uc.GetHistory(context.Background(), user1, vaultID, "notes/shared.md")
	require.NoError(t, err)
	assert.Len(t, history, 2)
	assert.Equal(t, user1, history[0].AuthorID)
	assert.Equal(t, user2, history[1].AuthorID)
}

func TestFileUsecase_EditorCanUpload(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleEditor)

	info := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/editor-file.md", []byte("content"))
	assert.Equal(t, 1, info.Version)
}

func uploadTestDelta(t *testing.T, deps *fileTestDeps, userID, deviceID, vaultID uuid.UUID, filePath string, baseVersion int, deltaData []byte) *usecase.FileInfo {
	t.Helper()
	info, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      filePath,
		BaseVersion:   baseVersion,
		Checksum:      "delta-checksum",
		SizeBytes:     100,
		EncryptedData: deltaData,
	})
	require.NoError(t, err)
	return info
}

func TestFileUsecase_DeltaPolicy_NeedsSnapshotAfterMaxDeltas(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 3,
		MaxDeltaSizeRatio:       0,
	})

	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("snapshot-v1"))

	info1 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 1, []byte("delta-1"))
	assert.False(t, info1.NeedsSnapshot)

	info2 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 2, []byte("delta-2"))
	assert.False(t, info2.NeedsSnapshot)

	info3 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 3, []byte("delta-3"))
	assert.True(t, info3.NeedsSnapshot)
}

func TestFileUsecase_DeltaPolicy_NeedsSnapshotBySizeRatio(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 0,
		MaxDeltaSizeRatio:       0.5,
	})

	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	snapshotContent := make([]byte, 1000)
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", snapshotContent)

	smallDelta := make([]byte, 200)
	info1 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 1, smallDelta)
	assert.False(t, info1.NeedsSnapshot)

	largeDelta := make([]byte, 400)
	info2 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 2, largeDelta)
	assert.True(t, info2.NeedsSnapshot)
}

func TestFileUsecase_DeltaPolicy_BothConditions(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 10,
		MaxDeltaSizeRatio:       0.5,
	})

	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	snapshotContent := make([]byte, 100)
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", snapshotContent)

	largeDelta := make([]byte, 60)
	info := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 1, largeDelta)
	assert.True(t, info.NeedsSnapshot)
}

func TestFileUsecase_DeltaPolicy_ResetAfterSnapshot(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 2,
		MaxDeltaSizeRatio:       0,
	})

	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("snapshot-v1"))

	uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 1, []byte("delta-1"))
	info2 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 2, []byte("delta-2"))
	assert.True(t, info2.NeedsSnapshot)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("snapshot-v4"))

	info4 := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 4, []byte("delta-after-snap"))
	assert.False(t, info4.NeedsSnapshot)
}

func TestFileUsecase_DeltaPolicy_NeedsSnapshotFalseOnSnapshotUpload(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	info := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("content"))
	assert.False(t, info.NeedsSnapshot)
}

func TestFileUsecase_SnapshotCleansUpOldDeltas(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("v1"))

	uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 1, []byte("delta-1"))
	uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 2, []byte("delta-2"))
	uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", 3, []byte("delta-3"))

	deltasBeforeSnapshot, err := deps.deltas.ListByFilePath(context.Background(), vaultID, "notes/test.md", 0)
	require.NoError(t, err)
	assert.Len(t, deltasBeforeSnapshot, 3)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("v5-snapshot"))

	deltasAfterSnapshot, err := deps.deltas.ListByFilePath(context.Background(), vaultID, "notes/test.md", 0)
	require.NoError(t, err)
	assert.Len(t, deltasAfterSnapshot, 0)
}

func TestFileUsecase_SnapshotCleansUpOnlyOldDeltas(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a-v1"))
	uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/a.md", 1, []byte("a-delta"))

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("b-v1"))
	uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/b.md", 1, []byte("b-delta"))

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("a-v3"))

	deltasA, err := deps.deltas.ListByFilePath(context.Background(), vaultID, "notes/a.md", 0)
	require.NoError(t, err)
	assert.Len(t, deltasA, 0)

	deltasB, err := deps.deltas.ListByFilePath(context.Background(), vaultID, "notes/b.md", 0)
	require.NoError(t, err)
	assert.Len(t, deltasB, 1)
}

func TestFileUsecase_DeltaPolicy_DisabledPolicy(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetDeltaPolicy(usecase.DeltaPolicy{
		MaxDeltasBeforeSnapshot: 0,
		MaxDeltaSizeRatio:       0,
	})

	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/test.md", []byte("snapshot-v1"))

	for i := 1; i <= 20; i++ {
		info := uploadTestDelta(t, deps, userID, deviceID, vaultID, "notes/test.md", i, []byte("delta"))
		assert.False(t, info.NeedsSnapshot)
	}
}

func TestFileUsecase_DeltaPolicy_DefaultPolicy(t *testing.T) {
	policy := usecase.DefaultDeltaPolicy()
	assert.Equal(t, 10, policy.MaxDeltasBeforeSnapshot)
	assert.Equal(t, 0.5, policy.MaxDeltaSizeRatio)
}

func TestFileUsecase_UploadSnapshot_DedupSameChecksum(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("content")
	info1 := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", content)
	assert.Equal(t, 1, info1.Version)

	info2, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/hello.md",
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, info2.Version)
	assert.Equal(t, info1.Checksum, info2.Checksum)
}

func TestFileUsecase_UploadSnapshot_DedupDifferentChecksum(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	info1 := uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))
	assert.Equal(t, 1, info1.Version)

	info2, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/hello.md",
		Checksum:    "different-hash",
		ContentType: "text/markdown",
		SizeBytes:   2,
		Data:        bytes.NewReader([]byte("v2")),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, info2.Version)
	assert.Equal(t, "different-hash", info2.Checksum)
}

func TestFileUsecase_UploadSnapshot_DedupEmptyChecksum(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", []byte("v1"))

	info2, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/hello.md",
		Checksum:    "",
		ContentType: "text/markdown",
		SizeBytes:   2,
		Data:        bytes.NewReader([]byte("v2")),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, info2.Version)
}

func TestFileUsecase_UploadSnapshot_DedupNoExisting(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	info, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/new.md",
		Checksum:    "hash1",
		ContentType: "text/markdown",
		SizeBytes:   5,
		Data:        bytes.NewReader([]byte("hello")),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, info.Version)
}

func TestFileUsecase_UploadSnapshot_DedupNoNewBlobCreated(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("content")
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", content)

	blobCountBefore := deps.blobs.Count()

	_, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/hello.md",
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	require.NoError(t, err)

	assert.Equal(t, blobCountBefore, deps.blobs.Count())
}

func TestFileUsecase_UploadSnapshot_DedupNoNewEventCreated(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("content")
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", content)

	events1, err := deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)

	_, err = deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/hello.md",
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	require.NoError(t, err)

	events2, err := deps.events.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, len(events1), len(events2))
}

func TestFileUsecase_UploadSnapshot_FileTooLarge(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxFileSize(100)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := make([]byte, 101)
	_, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/large.md",
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	assert.ErrorIs(t, err, domain.ErrFileTooLarge)
}

func TestFileUsecase_UploadSnapshot_AtSizeLimitPasses(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxFileSize(100)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := make([]byte, 100)
	info, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/exact.md",
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, info.Version)
}

func TestFileUsecase_UploadSnapshot_ZeroMaxFileSizeDisablesCheck(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxFileSize(0)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := make([]byte, 10*1024*1024)
	info, err := deps.uc.UploadSnapshot(context.Background(), usecase.UploadSnapshotInput{
		UserID:      userID,
		DeviceID:    deviceID,
		VaultID:     vaultID,
		FilePath:    "notes/huge.md",
		Checksum:    contentChecksum(content),
		ContentType: "text/markdown",
		SizeBytes:   int64(len(content)),
		Data:        bytes.NewReader(content),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, info.Version)
}

func TestFileUsecase_UploadDelta_FileTooLarge(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxFileSize(100)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	content := []byte("base content")
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/hello.md", content)

	largeDelta := make([]byte, 101)
	_, err := deps.uc.UploadDelta(context.Background(), usecase.UploadDeltaInput{
		UserID:        userID,
		DeviceID:      deviceID,
		VaultID:       vaultID,
		FilePath:      "notes/hello.md",
		BaseVersion:   1,
		Checksum:      "newchecksum",
		SizeBytes:     200,
		EncryptedData: largeDelta,
	})
	assert.ErrorIs(t, err, domain.ErrFileTooLarge)
}

func TestFileUsecase_UploadSnapshot_PrunesOldSnapshots(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxSnapshotsPerFile(3)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	for i := 0; i < 5; i++ {
		content := []byte("version content " + string(rune('0'+i)))
		uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/prune.md", content)
	}

	count := deps.snapshots.CountForFile(vaultID, "notes/prune.md")
	assert.Equal(t, 3, count)

	history, err := deps.uc.GetHistory(context.Background(), userID, vaultID, "notes/prune.md")
	require.NoError(t, err)
	assert.Equal(t, 3, len(history))
	assert.Equal(t, 3, history[0].Version)
	assert.Equal(t, 4, history[1].Version)
	assert.Equal(t, 5, history[2].Version)
}

func TestFileUsecase_UploadSnapshot_PruneDeletesBlobs(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxSnapshotsPerFile(2)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	for i := 0; i < 4; i++ {
		content := []byte("blob content " + string(rune('0'+i)))
		uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/blobprune.md", content)
	}

	assert.Equal(t, 2, deps.blobs.Count())
}

func TestFileUsecase_UploadSnapshot_ZeroMaxSnapshotsDisablesPruning(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxSnapshotsPerFile(0)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	for i := 0; i < 5; i++ {
		content := []byte("no prune content " + string(rune('0'+i)))
		uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/noprune.md", content)
	}

	count := deps.snapshots.CountForFile(vaultID, "notes/noprune.md")
	assert.Equal(t, 5, count)
}

func TestFileUsecase_UploadSnapshot_PruneDoesNotAffectOtherFiles(t *testing.T) {
	deps := setupFileTest()
	deps.uc.SetMaxSnapshotsPerFile(2)
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	for i := 0; i < 4; i++ {
		content := []byte("file-a content " + string(rune('0'+i)))
		uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/file-a.md", content)
	}

	contentB := []byte("file-b only version")
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/file-b.md", contentB)

	countA := deps.snapshots.CountForFile(vaultID, "notes/file-a.md")
	countB := deps.snapshots.CountForFile(vaultID, "notes/file-b.md")
	assert.Equal(t, 2, countA)
	assert.Equal(t, 1, countB)
}

func TestFileUsecase_BulkGetFileInfo_ReturnsExistingFiles(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/a.md", []byte("content a"))
	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/b.md", []byte("content b"))

	files, err := deps.uc.BulkGetFileInfo(context.Background(), usecase.BulkGetFileInfoInput{
		UserID:    userID,
		VaultID:   vaultID,
		FilePaths: []string{"notes/a.md", "notes/b.md"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(files))
}

func TestFileUsecase_BulkGetFileInfo_OmitsMissingFiles(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	deviceID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, deviceID, vaultID, "notes/exists.md", []byte("content"))

	files, err := deps.uc.BulkGetFileInfo(context.Background(), usecase.BulkGetFileInfoInput{
		UserID:    userID,
		VaultID:   vaultID,
		FilePaths: []string{"notes/exists.md", "notes/missing.md"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(files))
	assert.Equal(t, "notes/exists.md", files[0].FilePath)
}

func TestFileUsecase_BulkGetFileInfo_EmptyPathsReturnsEmpty(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	files, err := deps.uc.BulkGetFileInfo(context.Background(), usecase.BulkGetFileInfoInput{
		UserID:    userID,
		VaultID:   vaultID,
		FilePaths: []string{},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, len(files))
}

func TestFileUsecase_BulkGetFileInfo_NotVaultMember(t *testing.T) {
	deps := setupFileTest()
	vaultID := uuid.New()
	userID := uuid.New()
	stranger := uuid.New()
	addTestMember(t, deps, vaultID, userID, domain.VaultRoleOwner)

	uploadTestSnapshot(t, deps, userID, uuid.New(), vaultID, "notes/a.md", []byte("content"))

	_, err := deps.uc.BulkGetFileInfo(context.Background(), usecase.BulkGetFileInfoInput{
		UserID:    stranger,
		VaultID:   vaultID,
		FilePaths: []string{"notes/a.md"},
	})
	assert.ErrorIs(t, err, domain.ErrVaultAccessDenied)
}
