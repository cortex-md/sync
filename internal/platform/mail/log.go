package mail

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/rs/zerolog/log"
)

type LogMailer struct {
	renderer *Renderer
}

func NewLogMailer(renderer *Renderer) *LogMailer {
	return &LogMailer{renderer: renderer}
}

func (m *LogMailer) Send(_ context.Context, email domain.OutboxEmail) error {
	rendered, err := m.renderer.Render(email.Template, email.Locale, email.Data)
	if err != nil {
		return err
	}
	log.Info().
		Str("to", email.To).
		Str("template", email.Template).
		Str("subject", rendered.Subject).
		Str("text", rendered.Text).
		Msg("email (log mailer)")
	return nil
}
