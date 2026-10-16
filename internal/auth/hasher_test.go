package auth_test

import (
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/auth"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasherUsesFastDigestForDerivedAuthKeys(t *testing.T) {
	hasher := auth.NewBcryptHasherWithCost(4)
	hash, err := hasher.HashFor(domain.KDFVersionArgon2, "derived-auth-key")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "sha256$"))
	assert.NoError(t, hasher.Compare(hash, "derived-auth-key"))
	assert.ErrorIs(t, hasher.Compare(hash, "other"), domain.ErrInvalidCredentials)
	assert.False(t, hasher.NeedsRehash(hash, domain.KDFVersionArgon2))
}

func TestHasherKeepsBcryptForLegacyPasswordsAndFlagsUpgrade(t *testing.T) {
	hasher := auth.NewBcryptHasherWithCost(4)
	hash, err := hasher.HashFor(domain.KDFVersionLegacy, "password123")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$2"))
	assert.NoError(t, hasher.Compare(hash, "password123"))
	assert.False(t, hasher.NeedsRehash(hash, domain.KDFVersionLegacy))
	assert.True(t, hasher.NeedsRehash(hash, domain.KDFVersionArgon2))
}

func TestHasherRejectsLegacyPasswordsOverBcryptLimit(t *testing.T) {
	_, err := auth.NewBcryptHasherWithCost(4).HashFor(domain.KDFVersionLegacy, strings.Repeat("a", 73))
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestHasherDummyHashNeverMatchesRealInput(t *testing.T) {
	hasher := auth.NewBcryptHasherWithCost(4)
	assert.Error(t, hasher.Compare(hasher.DummyHash(), "anything"))
}
