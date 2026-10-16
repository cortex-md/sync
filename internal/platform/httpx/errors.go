package httpx

import (
	"errors"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
)

type domainError struct {
	err     error
	status  int
	code    string
	message string
}

var domainErrors = []domainError{
	{domain.ErrInvalidInput, http.StatusBadRequest, "invalid_input", "invalid input"},
	{domain.ErrNotFound, http.StatusNotFound, "not_found", "not found"},
	{domain.ErrAlreadyExists, http.StatusConflict, "already_exists", "already exists"},
	{domain.ErrConflict, http.StatusConflict, "conflict", "conflict"},
	{domain.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials", "invalid credentials"},
	{domain.ErrVaultAccessDenied, http.StatusForbidden, "vault_access_denied", "vault access denied"},
	{domain.ErrInsufficientRole, http.StatusForbidden, "insufficient_role", "insufficient permissions"},
	{domain.ErrFileTooLarge, http.StatusRequestEntityTooLarge, "file_too_large", "payload too large"},
	{domain.ErrDeltaTooLarge, http.StatusRequestEntityTooLarge, "delta_too_large", "delta too large, upload a snapshot instead"},
	{domain.ErrQuotaExceeded, http.StatusInsufficientStorage, "quota_exceeded", "vault storage quota exceeded"},
	{domain.ErrFeatureDisabled, http.StatusNotFound, "feature_disabled", "feature disabled on this server"},
	{domain.ErrKeysNotSetUp, http.StatusConflict, "keys_not_set_up", "account keys are not set up"},
	{domain.ErrEmailNotVerified, http.StatusForbidden, "email_not_verified", "verify your email address to use email invites"},
	{domain.ErrKeyExists, http.StatusConflict, "key_exists", "a vault key is already granted to this member"},
	{domain.ErrLimitReached, http.StatusForbidden, "limit_reached", "account limit reached"},
	{domain.ErrServerBusy, http.StatusServiceUnavailable, "server_busy", "server is busy, retry shortly"},
	{domain.ErrTooManyAttempts, http.StatusTooManyRequests, "too_many_attempts", "too many attempts, try again later"},
	{domain.ErrCursorExpired, http.StatusGone, "cursor_expired", "cursor is older than the retained history, run a full resync"},
	{domain.ErrInviteExpired, http.StatusGone, "invite_expired", "invite has expired"},
	{domain.ErrDomainNotAllowed, http.StatusForbidden, "domain_not_allowed", "email domain is not allowed in this vault"},
	{domain.ErrPublicLinksDisabled, http.StatusForbidden, "public_links_disabled", "public links are disabled for this vault"},
	{domain.ErrSubscriptionRequired, http.StatusPaymentRequired, "subscription_required", "subscription required"},
	{domain.ErrSubscriptionExpired, http.StatusPaymentRequired, "subscription_expired", "subscription expired"},
	{domain.ErrRegistrationClosed, http.StatusForbidden, "registration_closed", "registration is closed"},
	{domain.ErrGroupNameTaken, http.StatusConflict, "group_name_taken", "a group with this name already exists"},
}

func WriteDomainError(w http.ResponseWriter, err error) bool {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		WriteErrorWithCode(w, http.StatusRequestEntityTooLarge, "request body too large", "file_too_large")
		return true
	}
	for _, known := range domainErrors {
		if errors.Is(err, known.err) {
			if known.status == http.StatusServiceUnavailable {
				w.Header().Set("Retry-After", "5")
			}
			WriteErrorWithCode(w, known.status, known.message, known.code)
			return true
		}
	}
	return false
}

func WriteInternalError(w http.ResponseWriter) {
	WriteErrorWithCode(w, http.StatusInternalServerError, "internal server error", "internal_error")
}
