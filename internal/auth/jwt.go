package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func init() {
	jwt.TimePrecision = time.Millisecond
}

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
	UserID   string `json:"uid"`
	Email    string `json:"email"`
	DeviceID string `json:"did,omitempty"`
}

func (g *JWTGenerator) GenerateAccessToken(userID uuid.UUID, email string, deviceID uuid.UUID) (string, error) {
	now := time.Now()
	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    g.issuer,
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(now.Add(g.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		UserID:   userID.String(),
		Email:    email,
		DeviceID: deviceID.String(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(g.secret)
}

func (g *JWTGenerator) ValidateAccessToken(tokenString string) (*httpx.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &accessClaims{}, func(token *jwt.Token) (any, error) {
		return g.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(g.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
	)
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

	deviceID, err := uuid.Parse(claims.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("invalid device ID in token")
	}

	var issuedAt time.Time
	if claims.IssuedAt != nil {
		issuedAt = claims.IssuedAt.Time
	}
	return &httpx.Claims{
		UserID:   userID,
		Email:    claims.Email,
		DeviceID: deviceID,
		IssuedAt: issuedAt,
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
