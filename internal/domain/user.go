package domain

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID                        uuid.UUID
	Email                     string
	PasswordHash              string
	DisplayName               string
	PublicKey                 []byte
	EmailVerifiedAt           *time.Time
	AvatarKey                 string
	Locale                    string
	IsInstanceAdmin           bool
	KDFVersion                int
	KDFSalt                   []byte
	EncryptedPrivateKey       []byte
	RecoveryWrappedPrivateKey []byte
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

const AvatarKeyPrefix = "avatars/"

func (u *User) AvatarURL() string {
	if u.AvatarKey == "" {
		return ""
	}
	return "/avatars/v1/" + u.AvatarKey[len(AvatarKeyPrefix):]
}

func SupportedLocale(locale string) string {
	switch locale {
	case "pt-BR", "pt", "pt_BR":
		return "pt-BR"
	default:
		return "en"
	}
}
