package mail

import (
	"errors"
	"time"
)

var ErrPermanent = errors.New("permanent mail failure")

type RetryAfterError struct {
	Err   error
	After time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }

func (e *RetryAfterError) Unwrap() error { return e.Err }
