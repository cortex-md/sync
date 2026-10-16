package auth

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "PreloginRequest", preloginRequest{})
	apidocstest.AssertMatches(t, "PreloginResponse", preloginResponse{})
	apidocstest.AssertMatches(t, "RegisterRequest", registerRequest{})
	apidocstest.AssertMatches(t, "RegisterResponse", registerResponse{})
	apidocstest.AssertMatches(t, "LoginRequest", loginRequest{})
	apidocstest.AssertMatches(t, "LoginResponse", loginResponse{})
	apidocstest.AssertMatches(t, "RefreshRequest", refreshRequest{})
	apidocstest.AssertMatches(t, "TokenPair", refreshResponse{})
	apidocstest.AssertMatches(t, "LogoutRequest", logoutRequest{})
}
