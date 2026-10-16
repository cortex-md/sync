package txhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAfterCommitRunsImmediatelyOutsideTransaction(t *testing.T) {
	ran := false
	AfterCommit(context.Background(), func() { ran = true })
	require.True(t, ran)
}

func TestAfterCommitWaitsForCommit(t *testing.T) {
	ctx, finish := Begin(context.Background())
	ran := 0
	AfterCommit(ctx, func() { ran++ })
	require.Zero(t, ran)
	finish(true)
	require.Equal(t, 1, ran)
	finish(true)
	require.Equal(t, 1, ran)
}

func TestAfterCommitDropsOnRollback(t *testing.T) {
	ctx, finish := Begin(context.Background())
	ran := false
	AfterCommit(ctx, func() { ran = true })
	finish(false)
	require.False(t, ran)
}
