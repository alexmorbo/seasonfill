package webhook

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func testStart() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) }

// validEvent is a fixed, well-formed Imported event so map/ dedup logic
// have real fields to work with.
func validEvent() webhook.Event {
	return webhook.Event{
		Type:         webhook.EventTypeImported,
		InstanceName: "main",
		SeriesID:     1,
		SeasonNumber: 2,
		DownloadID:   "abc",
		RawEventType: "Download",
	}
}

func mapValid(_ []byte, _ domain.InstanceName) (webhook.Event, error) {
	return validEvent(), nil
}

// oneShotClaim returns `rows` on its first call and nil thereafter, so
// drainOnce's claim loop terminates.
func oneShotClaim(rows []ports.WebhookInboxRow) func(context.Context, time.Time, time.Time, int) ([]ports.WebhookInboxRow, error) {
	var done atomic.Bool
	return func(context.Context, time.Time, time.Time, int) ([]ports.WebhookInboxRow, error) {
		if done.Swap(true) {
			return nil, nil
		}
		return rows, nil
	}
}

func newDrainer(t *testing.T, mock *ports.WebhookInboxRepositoryMock, proc ProcessFunc, fake *clock.Fake) *Drainer {
	t.Helper()
	if mock.ReclaimStaleFunc == nil {
		mock.ReclaimStaleFunc = func(context.Context, time.Time) (int64, error) { return 0, nil }
	}
	return NewDrainer(DrainerDeps{
		Inbox:         mock,
		Process:       proc,
		MapEvent:      mapValid,
		Clock:         fake,
		AttemptCap:    3,
		PerJobTimeout: 5 * time.Second,
		Tick:          2 * time.Second,
		ClaimLimit:    50,
	})
}

func TestDrainer_Success_MarksSuccess(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:    oneShotClaim([]ports.WebhookInboxRow{{ID: 1, InstanceName: "main", EventType: "Download", Payload: []byte("{}")}}),
		MarkSuccessFunc: func(context.Context, int64) error { return nil },
	}
	d := newDrainer(t, mock, func(context.Context, webhook.Event) error { return nil }, fake)

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkSuccessCalls(), 1)
	assert.Equal(t, int64(1), mock.MarkSuccessCalls()[0].ID)
	assert.Empty(t, mock.MarkFailureCalls())
	assert.Empty(t, mock.MarkDeadCalls())
}

func TestDrainer_TransientUnderCap_MarksFailureEscalating(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())

	// Return the row with attempts=0 on pass 1, attempts=1 on pass 2.
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc: func(_ context.Context, _ time.Time, _ time.Time, _ int) ([]ports.WebhookInboxRow, error) {
			// each drainOnce call issues claims until empty; give exactly one row per pass.
			return nil, nil // replaced per-pass below
		},
		MarkFailureFunc: func(context.Context, int64, string, time.Time) error { return nil },
	}
	// Per-pass one-shot claim: rebuild ClaimDueFunc for each drainOnce.
	claimFor := func(attempts int) {
		mock.ClaimDueFunc = oneShotClaim([]ports.WebhookInboxRow{{ID: 7, InstanceName: "main", EventType: "Download", Payload: []byte("{}"), Attempts: attempts}})
	}
	proc := func(context.Context, webhook.Event) error { return ports.ErrDBUnavailable }
	d := newDrainer(t, mock, proc, fake)

	claimFor(0)
	d.drainOnce(context.Background())
	claimFor(1)
	d.drainOnce(context.Background())

	require.Len(t, mock.MarkFailureCalls(), 2)
	assert.Empty(t, mock.MarkDeadCalls())
	// backoffFor(1)=2s, backoffFor(2)=4s; fake clock does not advance.
	assert.Equal(t, testStart().Add(2*time.Second), mock.MarkFailureCalls()[0].NextAttemptAt)
	assert.Equal(t, testStart().Add(4*time.Second), mock.MarkFailureCalls()[1].NextAttemptAt)
}

func TestDrainer_CeilingReached_MarksDead(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	// AttemptCap=3; row.Attempts=2 -> attempt 3, 3<3 false -> dead.
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc: oneShotClaim([]ports.WebhookInboxRow{{ID: 9, InstanceName: "main", EventType: "Download", Payload: []byte("{}"), Attempts: 2}}),
		MarkDeadFunc: func(context.Context, int64, string) error { return nil },
	}
	proc := func(context.Context, webhook.Event) error { return ports.ErrDBUnavailable }
	d := newDrainer(t, mock, proc, fake)

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkDeadCalls(), 1)
	assert.Equal(t, int64(9), mock.MarkDeadCalls()[0].ID)
	assert.Empty(t, mock.MarkFailureCalls())
}

func TestDrainer_NonRetryableLogicError_MarksDeadImmediately(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc: oneShotClaim([]ports.WebhookInboxRow{{ID: 3, InstanceName: "main", EventType: "Download", Payload: []byte("{}"), Attempts: 0}}),
		MarkDeadFunc: func(context.Context, int64, string) error { return nil },
	}
	proc := func(context.Context, webhook.Event) error { return errors.New("logic boom") }
	d := newDrainer(t, mock, proc, fake)

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkDeadCalls(), 1)
	assert.Empty(t, mock.MarkFailureCalls())
}

func TestDrainer_OutboundSonarrTransient_Retries(t *testing.T) {
	t.Parallel()
	cases := map[string]error{
		"ErrInstanceNetwork": sharedErrors.ErrInstanceNetwork,
		"StatusError5xx":     &sonarr.StatusError{Endpoint: "/parse", Status: 503, Body: "down"},
		"ErrUnauthorized":    sharedErrors.ErrInstanceUnauthorized,
	}
	for name, retErr := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := clock.NewFake(testStart())
			mock := &ports.WebhookInboxRepositoryMock{
				ClaimDueFunc:    oneShotClaim([]ports.WebhookInboxRow{{ID: 5, InstanceName: "main", EventType: "Download", Payload: []byte("{}"), Attempts: 0}}),
				MarkFailureFunc: func(context.Context, int64, string, time.Time) error { return nil },
			}
			proc := func(context.Context, webhook.Event) error { return retErr }
			d := newDrainer(t, mock, proc, fake)

			d.drainOnce(context.Background())

			require.Len(t, mock.MarkFailureCalls(), 1, "outbound transient must retry")
			assert.Empty(t, mock.MarkDeadCalls())
		})
	}
}

func TestDrainer_ReclaimStale_CalledEachPass(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:     oneShotClaim(nil),
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
	}
	d := newDrainer(t, mock, func(context.Context, webhook.Event) error { return nil }, fake)

	d.drainOnce(context.Background())

	require.Len(t, mock.ReclaimStaleCalls(), 1)
	assert.Equal(t, testStart(), mock.ReclaimStaleCalls()[0].Now)
}

func TestDrainer_DuplicateSamePass_SkipsSecondProcess(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	// Two rows mapping to the SAME event (mapValid is constant) in one
	// batch -> second is a same-pass duplicate -> Process called once.
	rows := []ports.WebhookInboxRow{
		{ID: 1, InstanceName: "main", EventType: "Download", Payload: []byte("{}")},
		{ID: 2, InstanceName: "main", EventType: "Download", Payload: []byte("{}")},
	}
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:    oneShotClaim(rows),
		MarkSuccessFunc: func(context.Context, int64) error { return nil },
	}
	var procCalls atomic.Int32
	proc := func(context.Context, webhook.Event) error { procCalls.Add(1); return nil }
	d := newDrainer(t, mock, proc, fake)

	d.drainOnce(context.Background())

	assert.Equal(t, int32(1), procCalls.Load(), "duplicate must not re-run Process")
	assert.Len(t, mock.MarkSuccessCalls(), 2, "both rows settle success")
}

func TestDrainer_MapError_DeadLetters(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
		ClaimDueFunc:     oneShotClaim([]ports.WebhookInboxRow{{ID: 4, InstanceName: "main", EventType: "Download", Payload: []byte("{bad")}}),
		MarkDeadFunc:     func(context.Context, int64, string) error { return nil },
	}
	var procCalls atomic.Int32
	d := NewDrainer(DrainerDeps{
		Inbox:   mock,
		Process: func(context.Context, webhook.Event) error { procCalls.Add(1); return nil },
		MapEvent: func([]byte, domain.InstanceName) (webhook.Event, error) {
			return webhook.Event{}, errors.New("bad payload")
		},
		Clock: fake,
	})
	d.drainOnce(context.Background())
	require.Len(t, mock.MarkDeadCalls(), 1)
	assert.Equal(t, int32(0), procCalls.Load(), "malformed payload never runs Process")
}

func TestDrainer_Poke_WakesLoopEarly(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())

	var armed atomic.Bool
	var consumed atomic.Bool
	firstReturned := make(chan struct{})
	done := make(chan int64, 1)

	mock := &ports.WebhookInboxRepositoryMock{
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
		ClaimDueFunc: func(context.Context, time.Time, time.Time, int) ([]ports.WebhookInboxRow, error) {
			if !armed.Load() {
				select {
				case <-firstReturned:
				default:
					close(firstReturned)
				}
				return nil, nil
			}
			if consumed.Swap(true) {
				return nil, nil
			}
			return []ports.WebhookInboxRow{{ID: 11, InstanceName: "main", EventType: "Download", Payload: []byte("{}")}}, nil
		},
		MarkSuccessFunc: func(_ context.Context, id int64) error { done <- id; return nil },
	}
	d := newDrainer(t, mock, func(context.Context, webhook.Event) error { return nil }, fake)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go d.RunForever(ctx)

	<-firstReturned // immediate first drain complete; loop now parked in select
	armed.Store(true)
	d.Poke()

	select {
	case id := <-done:
		assert.Equal(t, int64(11), id)
	case <-time.After(3 * time.Second):
		t.Fatal("poke did not drive a drain")
	}
}

func TestDrainer_PerJobTimeout_CancelsJobNotParent(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())

	processStarted := make(chan struct{})
	mock := &ports.WebhookInboxRepositoryMock{
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
		ClaimDueFunc:     oneShotClaim([]ports.WebhookInboxRow{{ID: 12, InstanceName: "main", EventType: "Download", Payload: []byte("{}")}}),
		MarkFailureFunc:  func(context.Context, int64, string, time.Time) error { return nil },
	}
	proc := func(ctx context.Context, _ webhook.Event) error {
		close(processStarted)
		<-ctx.Done() // block until the per-job timer cancels us
		return ctx.Err()
	}
	d := NewDrainer(DrainerDeps{
		Inbox:         mock,
		Process:       proc,
		MapEvent:      mapValid,
		Clock:         fake,
		AttemptCap:    3,
		PerJobTimeout: 5 * time.Second,
	})

	parent := context.Background() // never cancelled -> proves it's the JOB timeout
	drainDone := make(chan struct{})
	go func() { d.drainOnce(parent); close(drainDone) }()

	<-processStarted
	fake.BlockUntilWaiters(1) // the per-job timer is parked
	fake.Advance(5 * time.Second)

	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatal("per-job timeout did not fire")
	}
	require.Len(t, mock.MarkFailureCalls(), 1, "per-job timeout -> retry (canceled is transient)")
	assert.Empty(t, mock.MarkDeadCalls())
}

// Sequential (NO t.Parallel): measures global-counter deltas.
func TestDrainer_Metrics_DeadOnlyOnDeadLetter(t *testing.T) {
	fake := clock.NewFake(testStart())

	before := scrape(t)
	beforeDead := readCounter(before, `seasonfill_webhook_inbox_dead_total`)
	beforeDeadOutcome := readCounter(before, `seasonfill_webhook_inbox_outcome_total{result="dead"}`)
	beforeRetry := readCounter(before, `seasonfill_webhook_inbox_outcome_total{result="retry"}`)
	beforeSuccess := readCounter(before, `seasonfill_webhook_inbox_outcome_total{result="success"}`)

	// mapDistinct assigns download IDs in claim order: ID 1 -> dl-b (retry,
	// transient), ID 2 -> dl-c (dead, logic), ID 3 -> dl-d (success).
	mock := &ports.WebhookInboxRepositoryMock{
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
		MarkSuccessFunc:  func(context.Context, int64) error { return nil },
		MarkFailureFunc:  func(context.Context, int64, string, time.Time) error { return nil },
		MarkDeadFunc:     func(context.Context, int64, string) error { return nil },
		ClaimDueFunc: oneShotClaim([]ports.WebhookInboxRow{
			{ID: 1, InstanceName: "a", EventType: "Download", Payload: []byte("{}"), Attempts: 0},
			{ID: 2, InstanceName: "b", EventType: "Download", Payload: []byte("{}"), Attempts: 0},
			{ID: 3, InstanceName: "c", EventType: "Download", Payload: []byte("{}"), Attempts: 0},
		}),
	}
	// Map each row to a DISTINCT event so the F-14 dedup doesn't collapse them.
	var n atomic.Int32
	mapDistinct := func([]byte, domain.InstanceName) (webhook.Event, error) {
		e := validEvent()
		e.DownloadID = "dl-" + string('a'+n.Add(1))
		return e, nil
	}
	proc := func(_ context.Context, e webhook.Event) error {
		switch e.DownloadID {
		case "dl-b":
			return ports.ErrDBUnavailable // retry
		case "dl-c":
			return errors.New("logic") // dead
		default:
			return nil // success
		}
	}
	d := NewDrainer(DrainerDeps{Inbox: mock, Process: proc, MapEvent: mapDistinct, Clock: fake, AttemptCap: 3, PerJobTimeout: 5 * time.Second})

	d.drainOnce(context.Background())

	after := scrape(t)
	assert.Equal(t, beforeDead+1, readCounter(after, `seasonfill_webhook_inbox_dead_total`), "dead_total only on dead-letter")
	assert.Equal(t, beforeDeadOutcome+1, readCounter(after, `seasonfill_webhook_inbox_outcome_total{result="dead"}`))
	assert.Equal(t, beforeRetry+1, readCounter(after, `seasonfill_webhook_inbox_outcome_total{result="retry"}`))
	assert.Equal(t, beforeSuccess+1, readCounter(after, `seasonfill_webhook_inbox_outcome_total{result="success"}`))
}

func scrape(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	observability.WritePrometheus(&b)
	return b.String()
}

// readCounter parses the float value for an exact metric series line
// (`<series> <value>`). Returns 0 when absent.
func readCounter(body, series string) float64 {
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, series+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}
