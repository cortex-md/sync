package mail_test

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/mail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRendererRendersEveryTemplateInEveryLocale(t *testing.T) {
	renderer, err := mail.NewRenderer()
	require.NoError(t, err)

	data := map[string]string{
		"DisplayName": "Ana",
		"InviterName": "Bruno",
		"VaultName":   `<script>alert(1)</script>`,
		"Role":        "editor",
		"NewEmail":    "new@example.com",
		"Link":        "https://sync.example.com/x?token=abc",
		"ExpiresAt":   "2026-10-18",
	}
	templates := []string{
		domain.EmailTemplateVerifyEmail, domain.EmailTemplateEmailChange, domain.EmailTemplateEmailChanged,
		domain.EmailTemplatePasswordReset, domain.EmailTemplatePasswordChanged, domain.EmailTemplateVaultInvite,
	}
	for _, locale := range []string{"en", "pt-BR"} {
		for _, name := range templates {
			rendered, err := renderer.Render(name, locale, data)
			require.NoError(t, err, "%s/%s", locale, name)
			assert.NotEmpty(t, rendered.Subject, "%s/%s", locale, name)
			assert.NotEmpty(t, rendered.Text, "%s/%s", locale, name)
			assert.NotContains(t, rendered.HTML, "<script>", "%s/%s must escape user data", locale, name)
		}
	}

	invite, err := renderer.Render(domain.EmailTemplateVaultInvite, "pt-BR", data)
	require.NoError(t, err)
	assert.Contains(t, invite.Text, "convidou")
	assert.Contains(t, invite.HTML, `href="https://sync.example.com/x?token=abc"`)
}
