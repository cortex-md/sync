package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeRawQueryRedactsSensitiveValues(t *testing.T) {
	result := sanitizeRawQuery("path=doc.md&token=jwt-secret-value&ticket=ticket-secret-value&refresh_token=refresh-secret-value&secret=secret-param-value")

	require.Contains(t, result, "path=doc.md")
	require.NotContains(t, result, "jwt-secret-value")
	require.NotContains(t, result, "ticket-secret-value")
	require.NotContains(t, result, "refresh-secret-value")
	require.NotContains(t, result, "secret-param-value")
	require.Contains(t, result, "%5Bredacted%5D")
}
