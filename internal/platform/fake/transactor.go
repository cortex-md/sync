package fake

import "context"

type Transactor struct{}

func NewTransactor() *Transactor {
	return &Transactor{}
}

func (t *Transactor) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}
