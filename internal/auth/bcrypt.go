package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

const (
	keyHashPrefix     = "sha256$"
	hashAcquireWait   = 3 * time.Second
	dummyKeyHashInput = "cortex-sync-dummy-auth-key"
)

type BcryptHasher struct {
	cost  int
	slots chan struct{}
}

func NewBcryptHasher() *BcryptHasher {
	return &BcryptHasher{cost: bcrypt.DefaultCost}
}

func NewBcryptHasherWithCost(cost int) *BcryptHasher {
	return &BcryptHasher{cost: cost}
}

func (h *BcryptHasher) WithConcurrency(limit int) *BcryptHasher {
	if limit > 0 {
		h.slots = make(chan struct{}, limit)
	}
	return h
}

func (h *BcryptHasher) HashFor(kdfVersion int, password string) (string, error) {
	if kdfVersion == domain.KDFVersionArgon2 {
		return hashAuthKey(password), nil
	}
	release, err := h.acquire()
	if err != nil {
		return "", err
	}
	defer release()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if errors.Is(err, bcrypt.ErrPasswordTooLong) {
		return "", domain.ErrInvalidInput
	}
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (h *BcryptHasher) Compare(hash string, password string) error {
	if stored, ok := strings.CutPrefix(hash, keyHashPrefix); ok {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(strings.TrimPrefix(hashAuthKey(password), keyHashPrefix))) == 1 {
			return nil
		}
		return domain.ErrInvalidCredentials
	}
	release, err := h.acquire()
	if err != nil {
		return err
	}
	defer release()
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func (h *BcryptHasher) NeedsRehash(hash string, kdfVersion int) bool {
	return kdfVersion == domain.KDFVersionArgon2 && !strings.HasPrefix(hash, keyHashPrefix)
}

func (h *BcryptHasher) DummyHash() string {
	return hashAuthKey(dummyKeyHashInput)
}

func (h *BcryptHasher) acquire() (func(), error) {
	if h.slots == nil {
		return func() {}, nil
	}
	timer := time.NewTimer(hashAcquireWait)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	case <-timer.C:
		return nil, domain.ErrServerBusy
	}
}

func hashAuthKey(authKey string) string {
	sum := sha256.Sum256([]byte(authKey))
	return keyHashPrefix + hex.EncodeToString(sum[:])
}
