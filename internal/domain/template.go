package domain

import (
	"time"

	"github.com/google/uuid"
)

type TemplateKind string

const (
	TemplateKindNote     TemplateKind = "note"
	TemplateKindDatabase TemplateKind = "database"
)

func (k TemplateKind) Valid() bool {
	return k == TemplateKindNote || k == TemplateKindDatabase
}

type TemplateAudience string

const (
	TemplateAudienceTeam   TemplateAudience = "team"
	TemplateAudienceLink   TemplateAudience = "link"
	TemplateAudienceMarket TemplateAudience = "market"
)

func (a TemplateAudience) Valid() bool {
	return a == TemplateAudienceTeam || a == TemplateAudienceLink || a == TemplateAudienceMarket
}

func (a TemplateAudience) Public() bool {
	return a == TemplateAudienceLink || a == TemplateAudienceMarket
}

type TemplateStatus string

const (
	TemplateStatusPublished     TemplateStatus = "published"
	TemplateStatusPendingReview TemplateStatus = "pending_review"
	TemplateStatusRejected      TemplateStatus = "rejected"
)

type TemplateShare struct {
	ID             uuid.UUID
	VaultID        uuid.UUID
	OwnerID        *uuid.UUID
	Kind           TemplateKind
	Audience       TemplateAudience
	GroupID        *uuid.UUID
	Name           string
	Description    string
	IncludeSamples bool
	Payload        []byte
	Encrypted      bool
	FileCount      int
	Version        int
	Status         TemplateStatus
	Slug           string
	ReviewNote     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TemplateListing struct {
	TemplateShare
	OwnerName string
}

func (t *TemplateShare) OwnedBy(userID uuid.UUID) bool {
	return t.OwnerID != nil && *t.OwnerID == userID
}

func (t *TemplateShare) PubliclyAvailable() bool {
	return t.Audience.Public() && t.Status == TemplateStatusPublished
}
