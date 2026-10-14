package bus

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	VaultEventMemberJoined      = "member_joined"
	VaultEventMemberRoleChanged = "member_role_changed"
	VaultEventMemberRemoved     = "member_removed"
	VaultEventKeyGranted        = "key_granted"
	VaultEventMemberKeyChanged  = "member_key_changed"
	VaultEventAccessChanged     = "access_changed"
	VaultEventCollabActive      = "collab_active"
	VaultEventCollabInactive    = "collab_inactive"
)

func PublishSignal(ctx context.Context, publisher Bus, signal Signal) {
	if publisher == nil {
		return
	}
	if err := publisher.Publish(ctx, signal); err != nil {
		log.Warn().Err(err).Str("kind", signal.Kind).Msg("failed to publish signal")
	}
}

func PublishVaultEvent(ctx context.Context, publisher Bus, vaultID uuid.UUID, event string, data map[string]any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	PublishSignal(ctx, publisher, Signal{Kind: KindSSE, VaultID: vaultID, Event: event, Data: payload})
}

func PublishAccessChanged(ctx context.Context, publisher Bus, vaultID, userID uuid.UUID) {
	PublishSignal(ctx, publisher, Signal{Kind: KindAccessChanged, VaultID: vaultID, UserID: userID})
}

func PublishVaultAccessChanged(ctx context.Context, publisher Bus, vaultID uuid.UUID) {
	PublishSignal(ctx, publisher, Signal{Kind: KindVaultAccessChanged, VaultID: vaultID})
}
