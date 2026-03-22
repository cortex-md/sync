package port

import (
	"context"
	"io"

	"github.com/google/uuid"
)

type BlobStorage interface {
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hash string, password string) error
}

type TokenGenerator interface {
	GenerateAccessToken(userID uuid.UUID, email string) (string, error)
	ValidateAccessToken(tokenString string) (*AccessTokenClaims, error)
	GenerateRefreshToken() (string, error)
	HashToken(token string) string
}

type AccessTokenClaims struct {
	UserID uuid.UUID
	Email  string
}

type SSEBroker interface {
	Subscribe(ctx context.Context, vaultID uuid.UUID, clientID string) (<-chan SSEEvent, error)
	Unsubscribe(vaultID uuid.UUID, clientID string)
	Publish(vaultID uuid.UUID, event SSEEvent)
}

type SSEEvent struct {
	ID        string
	EventType string
	Data      string
}

type CollabMessage struct {
	ClientID string
	Data     []byte
}

type CollabBroker interface {
	Join(vaultID uuid.UUID, filePath string, clientID string, ch chan<- CollabMessage) (newCount int, err error)
	Leave(vaultID uuid.UUID, filePath string, clientID string)
	Broadcast(vaultID uuid.UUID, filePath string, senderID string, data []byte)
	BufferUpdate(vaultID uuid.UUID, filePath string, data []byte)
	FlushUpdates(vaultID uuid.UUID, filePath string) [][]byte
	PeerCount(vaultID uuid.UUID, filePath string) int
	PeerIDs(vaultID uuid.UUID, filePath string) []string
}

type SubscriptionGateway interface {
	CreateCustomer(ctx context.Context, email string) (customerID string, err error)
	CreateSubscriptionCheckout(ctx context.Context, customerID string, returnURL string) (checkoutURL string, err error)
	GetSubscriptionStatus(ctx context.Context, subscriptionID string) (status string, err error)
}
