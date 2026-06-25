package usecase_test

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/port"
)

type recordingDevOpsNotifier struct {
	events []port.DevOpsEvent
	err    error
}

func (n *recordingDevOpsNotifier) Notify(_ context.Context, event port.DevOpsEvent) error {
	n.events = append(n.events, event)
	return n.err
}
