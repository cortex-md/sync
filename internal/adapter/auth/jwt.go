package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type JWTGenerator struct {
	secret       []byte
	accessExpiry time.Duration
	issuer       string
}

func NewJWTGenerator(secret string, accessExpiry time.Duration, issuer string) *JWTGenerator {
	return &JWTGenerator{
		secret:       []byte(secret),
		accessExpiry: accessExpiry,
		issuer:       issuer,
	}
}

type accessClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"uid"`
	Email  string `json:"email"`
}

func (g *JWTGenerator) GenerateAccessToken(userID uuid.UUID, email string) (string, error) {
	now := time.Now()
	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    g.issuer,
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(now.Add(g.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		UserID: userID.String(),
		Email:  email,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(g.secret)
}

func (g *JWTGenerator) ValidateAccessToken(tokenString string) (*port.AccessTokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &accessClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return g.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*accessClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID in token")
	}

	return &port.AccessTokenClaims{
		UserID: userID,
		Email:  claims.Email,
	}, nil
}

func (g *JWTGenerator) GenerateRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (g *JWTGenerator) HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
