package share

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "Share", shareResponse{})
	apidocstest.AssertMatches(t, "CreateShareRequest", createShareRequest{})
	apidocstest.AssertMatches(t, "UpdateShareRequest", updateShareRequest{})
}
