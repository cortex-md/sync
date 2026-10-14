package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func SubscriptionInvalidationHandler(invalidate func(uuid.UUID)) NotificationHandler {
	return func(_ context.Context, payload string) {
		var notification struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(payload), &notification); err != nil {
			log.Warn().Err(err).Str("payload", payload).Msg("invalid subscription pg notify payload")
			return
		}
		userID, err := uuid.Parse(notification.UserID)
		if err != nil {
			log.Warn().Err(err).Str("user_id", notification.UserID).Msg("invalid subscription user id in pg notify")
			return
		}
		invalidate(userID)
	}
}
