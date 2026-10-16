package file

import (
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/google/uuid"
)

const (
	BatchMaxItems             = 200
	BatchMaxBytes       int64 = 64 << 20
	maxListPageSize           = 5000
	defaultListPageSize       = 1000
	maxHistoryEntries         = 200
	maxDeltasPageSize         = 200
)

type Service struct {
	snapshots           FileSnapshotRepository
	deltas              FileDeltaRepository
	latest              FileLatestRepository
	events              SyncEventRepository
	devices             DeviceRepository
	vaults              VaultStorage
	blobs               BlobStorage
	blobQueue           BlobQueue
	publisher           EventPublisher
	tx                  Transactor
	authz               *authz.Authorizer
	collabDocs          CollabDocumentRepository
	shares              SharePaths
	access              AccessPaths
	deltaPolicy         DeltaPolicy
	maxFileSize         int64
	maxDeltaBytes       int64
	maxSnapshotsPerFile int
	enforceQuota        bool
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

type EventPublisher interface {
	PublishSyncEvent(event *domain.SyncEvent)
}

type Deps struct {
	Snapshots           FileSnapshotRepository
	Deltas              FileDeltaRepository
	Latest              FileLatestRepository
	Events              SyncEventRepository
	Members             VaultMemberRepository
	Users               UserRepository
	Devices             DeviceRepository
	Vaults              VaultStorage
	Blobs               BlobStorage
	BlobQueue           BlobQueue
	Tx                  Transactor
	Authz               *authz.Authorizer
	Publisher           EventPublisher
	CollabDocs          CollabDocumentRepository
	Shares              SharePaths
	Access              AccessPaths
	DeltaPolicy         *DeltaPolicy
	MaxFileSize         int64
	MaxDeltaBytes       int64
	MaxSnapshotsPerFile int
	EnforceQuota        bool
}

func NewService(d Deps) *Service {
	policy := DefaultDeltaPolicy()
	if d.DeltaPolicy != nil {
		policy = *d.DeltaPolicy
	}
	authorizer := d.Authz
	if authorizer == nil {
		authorizer = authz.New(d.Members)
	}
	return &Service{
		snapshots:           d.Snapshots,
		deltas:              d.Deltas,
		latest:              d.Latest,
		events:              d.Events,
		devices:             d.Devices,
		vaults:              d.Vaults,
		blobs:               d.Blobs,
		blobQueue:           d.BlobQueue,
		publisher:           d.Publisher,
		tx:                  d.Tx,
		authz:               authorizer,
		collabDocs:          d.CollabDocs,
		shares:              d.Shares,
		access:              d.Access,
		deltaPolicy:         policy,
		maxFileSize:         d.MaxFileSize,
		maxDeltaBytes:       d.MaxDeltaBytes,
		maxSnapshotsPerFile: d.MaxSnapshotsPerFile,
		enforceQuota:        d.EnforceQuota,
	}
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
	CreatedBy      uuid.UUID
	UpdatedAt      time.Time
	CreatedAt      time.Time
	Unchanged      bool
}

func fileInfoFromLatest(fl *domain.FileLatest) *FileInfo {
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
		CreatedBy:      fl.CreatedBy,
		UpdatedAt:      fl.UpdatedAt,
		CreatedAt:      fl.CreatedAt,
	}
}

func (uc *Service) publishSSE(event *domain.SyncEvent) {
	if uc.publisher == nil || event == nil {
		return
	}
	uc.publisher.PublishSyncEvent(event)
}
