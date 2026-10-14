package auth_test

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/devops"
)

type recordingDevOpsNotifier struct {
	events []devops.Event
	err    error
}

func (n *recordingDevOpsNotifier) Notify(_ context.Context, event devops.Event) error {
	n.events = append(n.events, event)
	return n.err
}
