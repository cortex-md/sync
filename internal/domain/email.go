package domain

import (
	"net/mail"
	"strings"
)

const MinPasswordLength = 8

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func ValidEmail(email string) bool {
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func (u *User) DisplayNameOrEmail() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Email
}
