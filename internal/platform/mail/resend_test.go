package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/mail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResend(t *testing.T, handler http.HandlerFunc) *mail.ResendMailer {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	renderer, err := mail.NewRenderer()
	require.NoError(t, err)
	mailer, err := mail.NewResendMailer(mail.ResendConfig{APIKey: "re_test", URL: server.URL, From: "Cortex <no-reply@example.com>", Timeout: time.Second}, renderer.WithPublicURL("https://sync.example.com"))
	require.NoError(t, err)
	return mailer
}

func outbox() domain.OutboxEmail {
	return domain.OutboxEmail{ID: 42, To: "user@example.com", Template: domain.EmailTemplatePasswordChanged, Locale: "en", Data: map[string]string{"Name": "Ana"}}
}

func TestResendSendsRenderedEmailWithIdempotencyKey(t *testing.T) {
	var got struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}
	mailer := newResend(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer re_test", r.Header.Get("Authorization"))
		assert.Equal(t, "cortex-outbox-42", r.Header.Get("Idempotency-Key"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
	})

	require.NoError(t, mailer.Send(context.Background(), outbox()))
	assert.Equal(t, []string{"user@example.com"}, got.To)
	assert.NotEmpty(t, got.Subject)
	assert.NotEmpty(t, got.Text)
}

func TestResendRateLimitIsRetryableWithRetryAfter(t *testing.T) {
	mailer := newResend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	err := mailer.Send(context.Background(), outbox())
	var retry *mail.RetryAfterError
	require.True(t, errors.As(err, &retry))
	assert.Equal(t, 30*time.Second, retry.After)
	assert.False(t, errors.Is(err, mail.ErrPermanent))
}

func TestResendClientErrorsArePermanent(t *testing.T) {
	mailer := newResend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	assert.ErrorIs(t, mailer.Send(context.Background(), outbox()), mail.ErrPermanent)
}

func TestResendServerErrorsAreRetryable(t *testing.T) {
	mailer := newResend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	err := mailer.Send(context.Background(), outbox())
	require.Error(t, err)
	assert.False(t, errors.Is(err, mail.ErrPermanent))
}
