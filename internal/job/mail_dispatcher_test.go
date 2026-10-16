package job_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/job"
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/platform/mail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scriptedMailer struct {
	mu    sync.Mutex
	sent  map[int64]int
	fail  func(email domain.OutboxEmail) error
	calls int
}

func (m *scriptedMailer) Send(_ context.Context, email domain.OutboxEmail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.fail != nil {
		if err := m.fail(email); err != nil {
			return err
		}
	}
	if m.sent == nil {
		m.sent = map[int64]int{}
	}
	m.sent[email.ID]++
	return nil
}

func (m *scriptedMailer) sentCount(id int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[id]
}

func enqueue(t *testing.T, outbox *fake.EmailOutboxRepository) int64 {
	t.Helper()
	email := &domain.OutboxEmail{To: "user@example.com", Template: "password_changed", Locale: "en"}
	require.NoError(t, outbox.Enqueue(context.Background(), email))
	return email.ID
}

func TestMailDispatcherSendsKickedEmailOnce(t *testing.T) {
	outbox := fake.NewEmailOutboxRepository()
	mailer := &scriptedMailer{}
	dispatcher := job.NewMailDispatcher(outbox, mailer)
	ctx, cancel := context.WithCancel(context.Background())
	go dispatcher.Run(ctx)

	id := enqueue(t, outbox)
	dispatcher.Kick(id)
	dispatcher.Kick(id)

	require.Eventually(t, func() bool { return mailer.sentCount(id) == 1 }, time.Second, 5*time.Millisecond)
	cancel()
	dispatcher.Wait()
	assert.Equal(t, 1, mailer.sentCount(id))
	assert.Empty(t, outbox.Pending())
}

func TestMailDispatcherBootSweepDeliversLeftovers(t *testing.T) {
	outbox := fake.NewEmailOutboxRepository()
	first, second := enqueue(t, outbox), enqueue(t, outbox)
	mailer := &scriptedMailer{}
	dispatcher := job.NewMailDispatcher(outbox, mailer)
	ctx, cancel := context.WithCancel(context.Background())
	go dispatcher.Run(ctx)

	require.Eventually(t, func() bool { return mailer.sentCount(first) == 1 && mailer.sentCount(second) == 1 }, time.Second, 5*time.Millisecond)
	cancel()
	dispatcher.Wait()
}

func TestMailDispatcherGivesUpOnPermanentErrors(t *testing.T) {
	outbox := fake.NewEmailOutboxRepository()
	id := enqueue(t, outbox)
	mailer := &scriptedMailer{fail: func(domain.OutboxEmail) error { return fmt.Errorf("%w: bad address", mail.ErrPermanent) }}

	sent, err := job.NewMailDispatcher(outbox, mailer).Sweep(context.Background())

	require.NoError(t, err)
	assert.EqualValues(t, 1, sent)
	assert.Empty(t, outbox.Pending())
	assert.Zero(t, mailer.sentCount(id))
}

func TestMailDispatcherSchedulesRetryForTransientErrors(t *testing.T) {
	outbox := fake.NewEmailOutboxRepository()
	id := enqueue(t, outbox)
	mailer := &scriptedMailer{fail: func(domain.OutboxEmail) error { return errors.New("connection reset") }}
	dispatcher := job.NewMailDispatcher(outbox, mailer)

	_, err := dispatcher.Sweep(context.Background())
	require.NoError(t, err)

	pending := outbox.Pending()
	require.Len(t, pending, 1)
	assert.Equal(t, id, pending[0].ID)
	assert.Equal(t, 1, pending[0].Attempts)
	assert.True(t, pending[0].NextAttemptAt.After(time.Now().Add(50*time.Second)))

	again, err := dispatcher.Sweep(context.Background())
	require.NoError(t, err)
	assert.Zero(t, again, "a scheduled retry is not claimable before its time")
}
