package server

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "VaultCursor", vaultCursorResponse{})
	apidocstest.AssertMatches(t, "OverviewDevice", overviewDevice{})
	apidocstest.AssertMatches(t, "AccountOverview", accountOverviewResponse{})
	apidocstest.AssertMatches(t, "MemberDevice", memberDevice{})
	apidocstest.AssertMatches(t, "PresencePeer", presencePeer{})
	apidocstest.AssertMatches(t, "VaultOverview", vaultOverviewResponse{})
}
