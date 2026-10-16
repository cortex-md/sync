package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details any    `json:"details,omitempty"`
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, ErrorResponse{Error: message, Code: codeForStatus(status)})
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_input"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusPaymentRequired:
		return "subscription_required"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "already_exists"
	case http.StatusGone:
		return "gone"
	case http.StatusRequestEntityTooLarge:
		return "file_too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "unavailable"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return ""
	}
}

func WriteErrorWithCode(w http.ResponseWriter, status int, message string, code string) {
	WriteJSON(w, status, ErrorResponse{Error: message, Code: code})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			WriteErrorWithCode(w, http.StatusRequestEntityTooLarge, "request body too large", "payload_too_large")
			return false
		}
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
