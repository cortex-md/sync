package auth_test

import (
	"context"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/auth"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withSecret(secret string) func(*auth.Deps) {
	return func(d *auth.Deps) { d.PreloginSecret = []byte(secret) }
}

func TestPreloginUnknownEmailReturnsDeterministicFakeSalt(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase(withSecret("secret-a"))
	ctx := context.Background()

	first, err := uc.Prelogin(ctx, "Nobody@Example.com")
	require.NoError(t, err)
	second, err := uc.Prelogin(ctx, " nobody@example.com ")
	require.NoError(t, err)
	other, err := uc.Prelogin(ctx, "someone@example.com")
	require.NoError(t, err)

	assert.Equal(t, domain.KDFVersionArgon2, first.KDFVersion)
	assert.Len(t, first.KDFSalt, domain.KDFSaltSize)
	assert.Equal(t, first.KDFSalt, second.KDFSalt)
	assert.NotEqual(t, first.KDFSalt, other.KDFSalt)
	assert.Equal(t, auth.FakePreloginSalt([]byte("secret-a"), "nobody@example.com"), first.KDFSalt)
	assert.NotEqual(t, auth.FakePreloginSalt([]byte("secret-b"), "nobody@example.com"), first.KDFSalt)
}

func TestPreloginReflectsAccountKDF(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase(withSecret("secret"))
	ctx := context.Background()

	_, err := uc.Register(ctx, auth.RegisterInput{Email: "legacy@example.com", Password: "password123", DisplayName: "Legacy"})
	require.NoError(t, err)
	legacy, err := uc.Prelogin(ctx, "legacy@example.com")
	require.NoError(t, err)
	assert.Equal(t, domain.KDFVersionLegacy, legacy.KDFVersion)
	assert.Empty(t, legacy.KDFSalt)

	salt := make([]byte, domain.KDFSaltSize)
	salt[0] = 42
	_, err = uc.Register(ctx, auth.RegisterInput{
		Email: "modern@example.com", Password: "derived-auth-key-0123456789", DisplayName: "Modern",
		KDFSalt: salt, PublicKey: make([]byte, domain.X25519KeySize), EncryptedPrivateKey: make([]byte, 60),
	})
	require.NoError(t, err)
	modern, err := uc.Prelogin(ctx, "MODERN@example.com")
	require.NoError(t, err)
	assert.Equal(t, domain.KDFVersionArgon2, modern.KDFVersion)
	assert.Equal(t, salt, modern.KDFSalt)
}

func TestRegisterRejectsPartialKeyMaterial(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()
	ctx := context.Background()
	cases := []auth.RegisterInput{
		{KDFSalt: make([]byte, domain.KDFSaltSize), PublicKey: make([]byte, domain.X25519KeySize)},
		{KDFSalt: make([]byte, 8), PublicKey: make([]byte, domain.X25519KeySize), EncryptedPrivateKey: make([]byte, 60)},
		{PublicKey: make([]byte, domain.X25519KeySize), EncryptedPrivateKey: make([]byte, 60)},
	}
	for _, in := range cases {
		in.Email, in.Password, in.DisplayName = "partial@example.com", "password123", "P"
		_, err := uc.Register(ctx, in)
		assert.ErrorIs(t, err, domain.ErrInvalidInput)
	}
}
