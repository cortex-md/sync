package domain

import "time"

const (
	EmailTemplateVerifyEmail     = "verify_email"
	EmailTemplateEmailChange     = "email_change"
	EmailTemplateEmailChanged    = "email_changed"
	EmailTemplatePasswordReset   = "password_reset"
	EmailTemplatePasswordChanged = "password_changed"
	EmailTemplateVaultInvite     = "vault_invite"
	EmailTemplateInviteDeclined  = "invite_declined"
)

type OutboxEmail struct {
	ID            int64
	To            string
	Template      string
	Locale        string
	Data          map[string]string
	Attempts      int
	NextAttemptAt time.Time
	CreatedAt     time.Time
}
