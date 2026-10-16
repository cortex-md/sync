package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"golang.org/x/time/rate"
)

const resendRequestsPerSecond = 2

type ResendConfig struct {
	APIKey  string
	URL     string
	From    string
	Timeout time.Duration
}

type ResendMailer struct {
	cfg      ResendConfig
	client   *http.Client
	limiter  *rate.Limiter
	renderer *Renderer
}

type resendPayload struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	HTML    string            `json:"html,omitempty"`
	Text    string            `json:"text"`
	Headers map[string]string `json:"headers,omitempty"`
}

func NewResendMailer(cfg ResendConfig, renderer *Renderer) (*ResendMailer, error) {
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("invalid mail from address: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &ResendMailer{
		cfg:      cfg,
		client:   &http.Client{Timeout: cfg.Timeout},
		limiter:  rate.NewLimiter(resendRequestsPerSecond, 1),
		renderer: renderer,
	}, nil
}

func (m *ResendMailer) Send(ctx context.Context, email domain.OutboxEmail) error {
	rendered, err := m.renderer.Render(email.Template, email.Locale, email.Data)
	if err != nil {
		return fmt.Errorf("%w: render %s: %v", ErrPermanent, email.Template, err)
	}
	to, err := mail.ParseAddress(email.To)
	if err != nil {
		return fmt.Errorf("%w: invalid recipient: %v", ErrPermanent, err)
	}
	body, err := json.Marshal(resendPayload{
		From:    m.cfg.From,
		To:      []string{to.Address},
		Subject: rendered.Subject,
		HTML:    rendered.HTML,
		Text:    rendered.Text,
		Headers: map[string]string{"X-Entity-Ref-ID": email.Template + "-" + strconv.FormatInt(email.ID, 10)},
	})
	if err != nil {
		return err
	}
	if err := m.limiter.Wait(ctx); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "cortex-outbox-"+strconv.FormatInt(email.ID, 10))

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend request: %w", err)
	}
	defer resp.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		failure := fmt.Errorf("resend responded %d: %s", resp.StatusCode, detail)
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			return &RetryAfterError{Err: failure, After: time.Duration(seconds) * time.Second}
		}
		return failure
	default:
		return fmt.Errorf("%w: resend responded %d: %s", ErrPermanent, resp.StatusCode, detail)
	}
}
