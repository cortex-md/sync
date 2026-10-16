package realtime

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "CollabPeer", collabPeer{})
	apidocstest.AssertMatches(t, "CollabMessage", collabMessage{})
	apidocstest.AssertMatches(t, "FilePresence", filePresence{})
	apidocstest.AssertMatches(t, "CollabTicketRequest", createCollabTicketRequest{})
	apidocstest.AssertMatches(t, "CollabTicket", createCollabTicketResponse{})
}
