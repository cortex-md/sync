package realtime_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/realtime"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketSigner_IsBoundToVaultPathAndSignature(t *testing.T) {
	signer := realtime.NewTicketSigner("secret", time.Minute)
	userID, vaultID := uuid.New(), uuid.New()

	ticket, _, err := signer.Issue(userID, vaultID, "notes/a.md")
	require.NoError(t, err)

	got, err := realtime.NewTicketSigner("secret", time.Minute).Verify(ticket, vaultID, "notes/a.md")
	require.NoError(t, err, "a ticket must be valid on any instance sharing the secret")
	assert.Equal(t, userID, got)

	_, err = signer.Verify(ticket, vaultID, "notes/b.md")
	assert.Error(t, err)
	_, err = signer.Verify(ticket, uuid.New(), "notes/a.md")
	assert.Error(t, err)
	_, err = realtime.NewTicketSigner("other-secret", time.Minute).Verify(ticket, vaultID, "notes/a.md")
	assert.Error(t, err)

	body, sig, _ := strings.Cut(ticket, ".")
	_, err = signer.Verify(body+"x."+sig, vaultID, "notes/a.md")
	assert.Error(t, err)

	expired, _, err := realtime.NewTicketSigner("secret", -time.Second).Issue(userID, vaultID, "notes/a.md")
	require.NoError(t, err)
	_, err = signer.Verify(expired, vaultID, "notes/a.md")
	assert.Error(t, err)
}
