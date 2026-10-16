package fake

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/platform/txhook"
)

type Transactor struct{}

func NewTransactor() *Transactor {
	return &Transactor{}
}

func (t *Transactor) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	hookCtx, finish := txhook.Begin(ctx)
	if err := fn(hookCtx); err != nil {
		return err
	}
	finish(true)
	return nil
}
