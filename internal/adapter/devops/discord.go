package devops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

const (
	defaultDiscordUsername = "Cortex DevOps"
	defaultDiscordTimeout  = 5 * time.Second

	maxDiscordTitleLength      = 256
	maxDiscordFieldNameLength  = 256
	maxDiscordFieldValueLength = 1024
	maxDiscordEmbedFields      = 25
	maxDiscordEmbedTotalLength = 6000

	discordColorBlue  = 0x3b82f6
	discordColorGreen = 0x22c55e
	discordColorAmber = 0xf59e0b
	discordColorRed   = 0xef4444
)

type DiscordNotifier struct {
	webhookURL  string
	username    string
	environment string
	http        *http.Client
}

func NewDiscordNotifier(webhookURL string, username string, timeout time.Duration, environment string) *DiscordNotifier {
	if username == "" {
		username = defaultDiscordUsername
	}
	if timeout <= 0 {
		timeout = defaultDiscordTimeout
	}
	return &DiscordNotifier{
		webhookURL:  strings.TrimSpace(webhookURL),
		username:    username,
		environment: environment,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (n *DiscordNotifier) Notify(ctx context.Context, event port.DevOpsEvent) error {
	if n == nil || n.webhookURL == "" {
		return nil
	}
	payload := n.buildPayload(event)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal discord devops payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create discord devops request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("send discord devops webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("discord devops webhook returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (n *DiscordNotifier) buildPayload(event port.DevOpsEvent) discordWebhookPayload {
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	environment := n.environment
	if environment == "" {
		environment = "unknown"
	}

	title := truncateDiscordValue(titleForEvent(event.Type), maxDiscordTitleLength)
	return discordWebhookPayload{
		Username: n.username,
		Embeds: []discordEmbed{
			{
				Title:     title,
				Color:     colorForEvent(event.Type),
				Timestamp: occurredAt.UTC().Format(time.RFC3339),
				Fields:    buildDiscordFields(environment, event, maxDiscordEmbedTotalLength-len([]rune(title))),
			},
		},
		AllowedMentions: discordAllowedMentions{
			Parse: []string{},
		},
	}
}

func buildDiscordFields(environment string, event port.DevOpsEvent, budget int) []discordEmbedField {
	fields := []discordEmbedField{
		{Name: "Event", Value: event.Type, Inline: true},
		{Name: "Environment", Value: environment, Inline: true},
		{Name: "User ID", Value: userIDField(event.UserID), Inline: false},
		{Name: "Email", Value: event.Email, Inline: true},
		{Name: "Display Name", Value: event.DisplayName, Inline: true},
	}

	keys := make([]string, 0, len(event.Fields))
	for key := range event.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(fields) >= maxDiscordEmbedFields {
			break
		}
		fields = append(fields, discordEmbedField{
			Name:   key,
			Value:  event.Fields[key],
			Inline: true,
		})
	}

	for index := range fields {
		fields[index].Name = truncateDiscordValue(fields[index].Name, maxDiscordFieldNameLength)
		fields[index].Value = truncateDiscordValue(fields[index].Value, maxDiscordFieldValueLength)
	}

	return constrainDiscordFields(fields, budget)
}

func constrainDiscordFields(fields []discordEmbedField, budget int) []discordEmbedField {
	if budget <= 0 {
		return nil
	}
	result := make([]discordEmbedField, 0, len(fields))
	for _, field := range fields {
		nameLength := len([]rune(field.Name))
		if budget <= nameLength {
			break
		}
		maxValueLength := budget - nameLength
		if maxValueLength < len([]rune(field.Value)) {
			field.Value = truncateDiscordValue(field.Value, maxValueLength)
		}
		fieldLength := nameLength + len([]rune(field.Value))
		if fieldLength > budget {
			break
		}
		result = append(result, field)
		budget -= fieldLength
	}
	return result
}

func titleForEvent(eventType string) string {
	switch eventType {
	case "account.created":
		return "Account created"
	case "auth.device_reassigned":
		return "Suspicious login: device reassigned"
	case "auth.revoked_device_reactivated":
		return "Suspicious login: revoked device reactivated"
	case "subscription.checkout_created":
		return "Subscription checkout created"
	case "subscription.completed":
		return "Subscription completed"
	case "subscription.renewed":
		return "Subscription renewed"
	case "subscription.cancelled":
		return "Subscription cancelled"
	case "subscription.trial_started":
		return "Subscription trial started"
	default:
		return eventType
	}
}

func colorForEvent(eventType string) int {
	switch eventType {
	case "account.created", "subscription.checkout_created":
		return discordColorBlue
	case "subscription.completed", "subscription.renewed", "subscription.trial_started":
		return discordColorGreen
	case "auth.device_reassigned", "auth.revoked_device_reactivated":
		return discordColorAmber
	case "subscription.cancelled":
		return discordColorRed
	default:
		return discordColorBlue
	}
}

func userIDField(userID uuid.UUID) string {
	if userID == uuid.Nil {
		return ""
	}
	return userID.String()
}

func truncateDiscordValue(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "none"
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	suffix := "..."
	if limit <= len(suffix) {
		return string(runes[:limit])
	}
	return string(runes[:limit-len(suffix)]) + suffix
}

type discordWebhookPayload struct {
	Username        string                 `json:"username,omitempty"`
	Embeds          []discordEmbed         `json:"embeds"`
	AllowedMentions discordAllowedMentions `json:"allowed_mentions"`
}

type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

type discordEmbed struct {
	Title     string              `json:"title"`
	Color     int                 `json:"color"`
	Timestamp string              `json:"timestamp"`
	Fields    []discordEmbedField `json:"fields"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}
