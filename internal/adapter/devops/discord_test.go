package devops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscordNotifierSendsWebhookPayload(t *testing.T) {
	var payload discordWebhookPayload
	var method string
	var contentType string
	var decodeErr error
	userID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		contentType = r.Header.Get("Content-Type")
		decodeErr = json.NewDecoder(r.Body).Decode(&payload)
		if decodeErr != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := NewDiscordNotifier(server.URL, "Cortex Ops", time.Second, "production")
	err := notifier.Notify(context.Background(), port.DevOpsEvent{
		Type:        "account.created",
		UserID:      userID,
		Email:       "user@example.com",
		DisplayName: "User Example",
		OccurredAt:  time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		Fields: map[string]string{
			"long_value": strings.Repeat("x", 2000),
			"source":     "test",
		},
	})

	require.NoError(t, err)
	require.NoError(t, decodeErr)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "application/json", contentType)
	require.Equal(t, "Cortex Ops", payload.Username)
	require.Len(t, payload.Embeds, 1)
	assert.Empty(t, payload.AllowedMentions.Parse)
	assert.Equal(t, "Account created", payload.Embeds[0].Title)
	assert.Equal(t, "2026-01-15T12:00:00Z", payload.Embeds[0].Timestamp)
	assert.Equal(t, "account.created", findDiscordField(payload.Embeds[0].Fields, "Event"))
	assert.Equal(t, "production", findDiscordField(payload.Embeds[0].Fields, "Environment"))
	assert.Equal(t, userID.String(), findDiscordField(payload.Embeds[0].Fields, "User ID"))
	assert.Equal(t, "user@example.com", findDiscordField(payload.Embeds[0].Fields, "Email"))
	assert.Equal(t, "User Example", findDiscordField(payload.Embeds[0].Fields, "Display Name"))
	assert.Equal(t, "test", findDiscordField(payload.Embeds[0].Fields, "source"))
	longValue := findDiscordField(payload.Embeds[0].Fields, "long_value")
	assert.LessOrEqual(t, len([]rune(longValue)), maxDiscordFieldValueLength)
	assert.True(t, strings.HasSuffix(longValue, "..."))
}

func TestDiscordNotifierReturnsErrorForRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	notifier := NewDiscordNotifier(server.URL, "", time.Second, "development")
	err := notifier.Notify(context.Background(), port.DevOpsEvent{Type: "account.created"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 429")
}

func TestDiscordNotifierReturnsErrorForTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := NewDiscordNotifier(server.URL, "", time.Millisecond, "development")
	err := notifier.Notify(context.Background(), port.DevOpsEvent{Type: "account.created"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "send discord devops webhook")
}

func findDiscordField(fields []discordEmbedField, name string) string {
	for _, field := range fields {
		if field.Name == name {
			return field.Value
		}
	}
	return ""
}
