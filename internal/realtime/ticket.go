package realtime

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type TicketSigner struct {
	key  []byte
	ttl  time.Duration
	mu   sync.Mutex
	used map[string]int64
}

type ticketClaims struct {
	UserID   uuid.UUID `json:"u"`
	VaultID  uuid.UUID `json:"v"`
	FilePath string    `json:"p"`
	Expires  int64     `json:"e"`
	Nonce    string    `json:"n,omitempty"`
}

func NewTicketSigner(secret string, ttl time.Duration) *TicketSigner {
	sum := sha256.Sum256([]byte("cortex-collab-ticket:" + secret))
	return &TicketSigner{key: sum[:], ttl: ttl, used: make(map[string]int64)}
}

func (s *TicketSigner) Issue(userID, vaultID uuid.UUID, filePath string) (string, time.Time, error) {
	expiresAt := time.Now().Add(s.ttl)
	payload, err := json.Marshal(ticketClaims{UserID: userID, VaultID: vaultID, FilePath: filePath, Expires: expiresAt.Unix(), Nonce: uuid.NewString()})
	if err != nil {
		return "", time.Time{}, err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.sign(body)), expiresAt, nil
}

func (s *TicketSigner) Verify(ticket string, vaultID uuid.UUID, filePath string) (uuid.UUID, error) {
	body, signature, ok := strings.Cut(ticket, ".")
	if !ok {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(given, s.sign(body)) {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	var claims ticketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	if time.Now().Unix() > claims.Expires || claims.VaultID != vaultID || claims.FilePath != filePath {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	if !s.consume(signature, claims.Expires) {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	return claims.UserID, nil
}

func (s *TicketSigner) consume(signature string, expires int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for key, expiry := range s.used {
		if expiry < now {
			delete(s.used, key)
		}
	}
	if _, seen := s.used[signature]; seen {
		return false
	}
	s.used[signature] = expires
	return true
}

func (s *TicketSigner) sign(body string) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return mac.Sum(nil)
}
