package lifecycle

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Lifecycle struct {
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	closing  bool
	streams  sync.WaitGroup
	draining atomic.Bool
}

func New() *Lifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &Lifecycle{ctx: ctx, cancel: cancel}
}

func (l *Lifecycle) Done() <-chan struct{} {
	return l.ctx.Done()
}

func (l *Lifecycle) Draining() bool {
	return l.draining.Load()
}

func (l *Lifecycle) TrackStream() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return func() {}
	}
	l.streams.Add(1)
	var once sync.Once
	return func() { once.Do(l.streams.Done) }
}

func (l *Lifecycle) StartDrain() {
	l.draining.Store(true)
}

func (l *Lifecycle) CloseStreams(timeout time.Duration) bool {
	l.mu.Lock()
	l.closing = true
	l.mu.Unlock()
	l.cancel()
	done := make(chan struct{})
	go func() {
		l.streams.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
