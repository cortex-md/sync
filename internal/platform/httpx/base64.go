package httpx

import (
	"encoding/base64"
	"errors"
	"strings"
)

var errInvalidBase64 = errors.New("invalid base64")

func DecodeBase64(value string) ([]byte, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "=")
	if trimmed == "" {
		return nil, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	return nil, errInvalidBase64
}

func DecodeOptionalBase64(value *string) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return DecodeBase64(*value)
}

func EncodeBase64(value []byte) *string {
	if len(value) == 0 {
		return nil
	}
	encoded := base64.StdEncoding.EncodeToString(value)
	return &encoded
}
