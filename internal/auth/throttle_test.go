package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoginThrottleLocksAfterThresholdAndBacksOff(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := NewLoginThrottle(3)
	throttle.now = func() time.Time { return now }

	for range 2 {
		throttle.Fail("a@b|1.1.1.1")
	}
	assert.Zero(t, throttle.Blocked("a@b|1.1.1.1"))

	throttle.Fail("a@b|1.1.1.1")
	assert.Equal(t, time.Minute, throttle.Blocked("a@b|1.1.1.1"))
	assert.Zero(t, throttle.Blocked("a@b|2.2.2.2"))

	now = now.Add(time.Minute)
	throttle.Fail("a@b|1.1.1.1")
	assert.Equal(t, 2*time.Minute, throttle.Blocked("a@b|1.1.1.1"))

	throttle.Succeed("a@b|1.1.1.1")
	assert.Zero(t, throttle.Blocked("a@b|1.1.1.1"))
}

func TestNilLoginThrottleIsDisabled(t *testing.T) {
	var throttle *LoginThrottle = NewLoginThrottle(0)
	throttle.Fail("x")
	assert.Zero(t, throttle.Blocked("x"))
}
