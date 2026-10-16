package account

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "Account", AccountResponse{})
	apidocstest.AssertMatches(t, "UpdateProfileRequest", updateProfileRequest{})
	apidocstest.AssertMatches(t, "ChangePasswordRequest", changePasswordRequest{})
	apidocstest.AssertMatches(t, "RequestEmailChangeRequest", requestEmailChangeRequest{})
	apidocstest.AssertMatches(t, "ForgotPasswordRequest", forgotPasswordRequest{})
	apidocstest.AssertMatches(t, "ResetPasswordRequest", tokenRequest{})
	apidocstest.AssertMatches(t, "ResetPrepareRequest", resetPrepareRequest{})
	apidocstest.AssertMatches(t, "ResetPrepareResponse", resetPrepareResponse{})
	apidocstest.AssertMatches(t, "AccountKeys", keysResponse{})
	apidocstest.AssertMatches(t, "SetupKeysRequest", setupKeysRequest{})
	apidocstest.AssertMatches(t, "RecoveryRequest", recoveryRequest{})
}
