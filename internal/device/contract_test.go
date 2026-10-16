package device

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "Device", DeviceResponse{})
	apidocstest.AssertMatches(t, "UpdateDeviceRequest", updateDeviceRequest{})
}
