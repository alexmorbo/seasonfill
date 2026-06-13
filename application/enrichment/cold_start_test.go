package enrichment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeScanner struct {
	ids  []int64
	pass int32
	err  error
}

func (f *fakeScanner) ListMissingSyncLog(_ context.Context, _ string, _ int) ([]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	if atomic.AddInt32(&f.pass, 1) == 1 {
		return f.ids, nil
	}
	return nil, nil
}

type recordedCall struct {
	Kind     EntityKind
	ID       int64
	Priority Priority
}

type recordingDispatcher struct {
	calls []recordedCall
}

func (r *recordingDispatcher) Enqueue(k EntityKind, id int64, p Priority) {
	r.calls = append(r.calls, recordedCall{Kind: k, ID: id, Priority: p})
}

func (r *recordingDispatcher) Close() {}

func TestBackfillSeries_IdempotentAfterFirstPass(t *testing.T) {
	t.Parallel()
	scanner := &fakeScanner{ids: []int64{10, 20, 30}}
	d := &recordingDispatcher{}
	ctx := context.Background()

	require.NoError(t, BackfillSeries(ctx, scanner, d, quietLogger()))
	require.Len(t, d.calls, 3)
	for i, c := range d.calls {
		assert.Equal(t, EntitySeries, c.Kind)
		assert.Equal(t, PriorityCold, c.Priority)
		assert.Equal(t, scanner.ids[i], c.ID)
	}

	// Second pass: scanner returns empty (every row now has a row).
	d2 := &recordingDispatcher{}
	require.NoError(t, BackfillSeries(ctx, scanner, d2, quietLogger()))
	assert.Empty(t, d2.calls)
}

func TestBackfillSeries_ScannerError_PropagatesWrapped(t *testing.T) {
	t.Parallel()
	scanner := &fakeScanner{err: errors.New("db down")}
	d := &recordingDispatcher{}
	err := BackfillSeries(context.Background(), scanner, d, quietLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cold-start scan")
	assert.Empty(t, d.calls)
}

func TestBackfillSeries_EmptyIDs_NoEnqueue(t *testing.T) {
	t.Parallel()
	scanner := &fakeScanner{ids: nil}
	d := &recordingDispatcher{}
	require.NoError(t, BackfillSeries(context.Background(), scanner, d, quietLogger()))
	assert.Empty(t, d.calls)
}
