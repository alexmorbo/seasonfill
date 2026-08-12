package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type stubNotifier struct {
	err   error
	calls int
}

func (s *stubNotifier) Send(_ context.Context, _ []byte, _ Message) error {
	s.calls++
	return s.err
}

func agent(id int64, name string) ports.NotificationAgent {
	return ports.NotificationAgent{ID: id, Name: name, Enabled: true, ConfigEncrypted: []byte{byte(id)}, EventTypes: []string{"grab.failed"}}
}

func newTestDispatcher(outbox *ports.OutboxRepositoryMock, agents *ports.NotificationAgentRepositoryMock, n Notifier, clk clock.Clock) *Dispatcher {
	return NewDispatcher(DispatcherDeps{
		Outbox: outbox, Agents: agents, Notifier: n, Clock: clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
	})
}

func dueRow(attempts int) ports.OutboxRow {
	return ports.OutboxRow{ID: 1, EventType: "grab.failed", Payload: []byte(`{"series_title":"X","season":1}`), Attempts: attempts}
}

func TestDispatcher_HappyPath_MarkSent(t *testing.T) {
	t.Parallel()
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(0)}, nil
		},
		MarkSentFunc: func(context.Context, int64) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{agent(1, "a")}, nil
		},
	}
	n := &stubNotifier{}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	assert.Equal(t, 1, n.calls)
	assert.Len(t, outbox.MarkSentCalls(), 1)
	assert.Empty(t, outbox.RescheduleCalls())
	assert.Empty(t, outbox.MarkDeadCalls())
}

func TestDispatcher_FanOut_TwoAgents(t *testing.T) {
	t.Parallel()
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(0)}, nil
		},
		MarkSentFunc: func(context.Context, int64) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{agent(1, "a"), agent(2, "b")}, nil
		},
	}
	n := &stubNotifier{}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	assert.Equal(t, 2, n.calls)
	assert.Len(t, outbox.MarkSentCalls(), 1)
}

func TestDispatcher_Retry_Reschedule(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(0)}, nil
		},
		RescheduleFunc: func(context.Context, int64, time.Time) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{agent(1, "a")}, nil
		},
	}
	n := &stubNotifier{err: errors.New("boom")}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(now))
	d.dispatchOnce(context.Background())

	require.Len(t, outbox.RescheduleCalls(), 1)
	assert.Empty(t, outbox.MarkDeadCalls())
	// next_attempt_at = now + backoffFor(1)
	assert.WithinDuration(t, now.Add(backoffFor(1)), outbox.RescheduleCalls()[0].NextAttemptAt, time.Millisecond)
}

func TestDispatcher_DLQ_MarkDead(t *testing.T) {
	t.Parallel()
	outbox := &ports.OutboxRepositoryMock{
		// attempts = cap-1 = 9 → attempts+1 (10) >= cap → dead.
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(9)}, nil
		},
		MarkDeadFunc: func(context.Context, int64) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{agent(1, "a")}, nil
		},
	}
	n := &stubNotifier{err: errors.New("boom")}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	assert.Len(t, outbox.MarkDeadCalls(), 1)
	assert.Empty(t, outbox.RescheduleCalls())
}

func TestDispatcher_NoSubscribers_Drop(t *testing.T) {
	t.Parallel()
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(0)}, nil
		},
		MarkSentFunc: func(context.Context, int64) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) { return nil, nil },
	}
	n := &stubNotifier{}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	assert.Equal(t, 0, n.calls)
	assert.Len(t, outbox.MarkSentCalls(), 1) // dropped as success
}

func TestDispatcher_PartialFailure_Reschedule(t *testing.T) {
	t.Parallel()
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(0)}, nil
		},
		RescheduleFunc: func(context.Context, int64, time.Time) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{agent(1, "a"), agent(2, "b")}, nil
		},
	}
	// Fail on the second agent only.
	var calls int
	n := &funcNotifier{fn: func() error {
		calls++
		if calls == 2 {
			return errors.New("second fails")
		}
		return nil
	}}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	assert.Len(t, outbox.RescheduleCalls(), 1)
	assert.Empty(t, outbox.MarkSentCalls())
}

func TestDispatcher_ListAgentsError_Reschedule(t *testing.T) {
	t.Parallel()
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{dueRow(0)}, nil
		},
		RescheduleFunc: func(context.Context, int64, time.Time) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(context.Context, string, int64) ([]ports.NotificationAgent, error) {
			return nil, errors.New("db down")
		},
	}
	n := &stubNotifier{}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	assert.Equal(t, 0, n.calls)
	assert.Len(t, outbox.RescheduleCalls(), 1)
}

// TestDispatcher_PerUser_NoCrossUserLeak proves the dispatcher selects agents
// by the outbox row's target user_id (Ф8-U-5c): a row for user 7 must query
// user 7 and deliver only to user 7's agents, never another user's.
func TestDispatcher_PerUser_NoCrossUserLeak(t *testing.T) {
	t.Parallel()
	row := ports.OutboxRow{ID: 1, UserID: 7, EventType: "season.premiere", Payload: []byte(`{}`)}
	outbox := &ports.OutboxRepositoryMock{
		FetchDueBatchFunc: func(context.Context, time.Time, int) ([]ports.OutboxRow, error) {
			return []ports.OutboxRow{row}, nil
		},
		MarkSentFunc: func(context.Context, int64) error { return nil },
	}
	agents := &ports.NotificationAgentRepositoryMock{
		ListEnabledForEventAndUserFunc: func(_ context.Context, _ string, userID int64) ([]ports.NotificationAgent, error) {
			if userID == 7 {
				return []ports.NotificationAgent{agent(70, "u7")}, nil
			}
			return []ports.NotificationAgent{agent(90, "other")}, nil
		},
	}
	n := &stubNotifier{}
	d := newTestDispatcher(outbox, agents, n, clock.NewFake(time.Now()))
	d.dispatchOnce(context.Background())

	require.Len(t, agents.ListEnabledForEventAndUserCalls(), 1)
	assert.EqualValues(t, 7, agents.ListEnabledForEventAndUserCalls()[0].UserID)
	assert.Equal(t, 1, n.calls) // only user 7's single agent
	assert.Len(t, outbox.MarkSentCalls(), 1)
}

type funcNotifier struct{ fn func() error }

func (f *funcNotifier) Send(context.Context, []byte, Message) error { return f.fn() }
