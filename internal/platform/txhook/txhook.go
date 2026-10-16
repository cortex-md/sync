package txhook

import (
	"context"
	"sync"
)

type keyType struct{}

var key = keyType{}

type hooks struct {
	mu  sync.Mutex
	fns []func()
}

func Begin(ctx context.Context) (context.Context, func(committed bool)) {
	h := &hooks{}
	return context.WithValue(ctx, key, h), func(committed bool) {
		if !committed {
			return
		}
		h.mu.Lock()
		fns := h.fns
		h.fns = nil
		h.mu.Unlock()
		for _, fn := range fns {
			fn()
		}
	}
}

func AfterCommit(ctx context.Context, fn func()) {
	if h, ok := ctx.Value(key).(*hooks); ok {
		h.mu.Lock()
		h.fns = append(h.fns, fn)
		h.mu.Unlock()
		return
	}
	fn()
}
