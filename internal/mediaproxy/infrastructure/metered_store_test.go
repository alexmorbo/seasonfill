package mediastore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

// meterFakeStore is a programmable Store double for the decorator tests.
// Each method records that it was called and returns the pre-seeded
// value/error so the test can assert both delegation and metric side
// effects.
type meterFakeStore struct {
	calls []string

	getInfo ObjectInfo
	getErr  error
	putErr  error
	statAt  ObjectInfo
	statErr error
	delErr  error
	listErr error
}

func (f *meterFakeStore) Get(_ context.Context, _ string) (io.ReadCloser, ObjectInfo, error) {
	f.calls = append(f.calls, "get")
	if f.getErr != nil {
		return nil, ObjectInfo{}, f.getErr
	}
	return io.NopCloser(bytes.NewReader([]byte("body"))), f.getInfo, nil
}

func (f *meterFakeStore) Put(_ context.Context, _ string, r io.Reader, _ int64, _ string) error {
	f.calls = append(f.calls, "put")
	_, _ = io.Copy(io.Discard, r)
	return f.putErr
}

func (f *meterFakeStore) Stat(_ context.Context, _ string) (ObjectInfo, error) {
	f.calls = append(f.calls, "stat")
	return f.statAt, f.statErr
}

func (f *meterFakeStore) Delete(_ context.Context, _ string) error {
	f.calls = append(f.calls, "delete")
	return f.delErr
}

func (f *meterFakeStore) List(_ context.Context, _ string, _ func(ObjectInfo) error) error {
	f.calls = append(f.calls, "list")
	return f.listErr
}

func TestMeteredStore_DelegatesAllMethods(t *testing.T) {
	fake := &meterFakeStore{getInfo: ObjectInfo{Size: 4}}
	m := NewMeteredStore(fake, 24, 12)
	ctx := context.Background()

	rc, info, err := m.Get(ctx, "k")
	require.NoError(t, err)
	require.NotNil(t, rc)
	_ = rc.Close()
	assert.Equal(t, int64(4), info.Size)

	require.NoError(t, m.Put(ctx, "k", bytes.NewReader([]byte("xy")), 2, "image/jpeg"))
	_, err = m.Stat(ctx, "k")
	require.NoError(t, err)
	require.NoError(t, m.Delete(ctx, "k"))
	require.NoError(t, m.List(ctx, "p", func(ObjectInfo) error { return nil }))

	assert.Equal(t, []string{"get", "put", "stat", "delete", "list"}, fake.calls)
}

func TestMeteredStore_PropagatesErrors(t *testing.T) {
	sentinel := errors.New("boom")
	fake := &meterFakeStore{getErr: sentinel, putErr: sentinel, statErr: sentinel, delErr: sentinel, listErr: sentinel}
	m := NewMeteredStore(fake, 24, 12)
	ctx := context.Background()

	_, _, err := m.Get(ctx, "k")
	assert.ErrorIs(t, err, sentinel)
	assert.ErrorIs(t, m.Put(ctx, "k", bytes.NewReader(nil), 0, ""), sentinel)
	_, err = m.Stat(ctx, "k")
	assert.ErrorIs(t, err, sentinel)
	assert.ErrorIs(t, m.Delete(ctx, "k"), sentinel)
	assert.ErrorIs(t, m.List(ctx, "p", nil), sentinel)
}

func TestMeteredStore_RecordsOutcomesAndBytes(t *testing.T) {
	ctx := context.Background()

	// ok get → records get/ok and 1234 bytes.
	okGet := NewMeteredStore(&meterFakeStore{getInfo: ObjectInfo{Size: 1234}}, 24, 12)
	rc, _, err := okGet.Get(ctx, "k")
	require.NoError(t, err)
	_ = rc.Close()

	// not_found stat → records stat/not_found.
	nfStat := NewMeteredStore(&meterFakeStore{statErr: fmt.Errorf("wrap: %w", ErrNotFound)}, 24, 12)
	_, _ = nfStat.Stat(ctx, "missing")

	// timeout get → records get/timeout (context.DeadlineExceeded chain).
	toGet := NewMeteredStore(&meterFakeStore{getErr: fmt.Errorf("wrap: %w", context.DeadlineExceeded)}, 24, 12)
	_, _, _ = toGet.Get(ctx, "slow")

	// minio 404 delete → response code 404 recorded.
	me := minio.ErrorResponse{StatusCode: 404, Code: "NoSuchKey"}
	codeDel := NewMeteredStore(&meterFakeStore{delErr: fmt.Errorf("wrap: %w", me)}, 24, 12)
	_ = codeDel.Delete(ctx, "gone")

	body := &bytes.Buffer{}
	observability.WritePrometheus(body)
	s := body.String()

	assert.Contains(t, s, `seasonfill_s3_requests_total{op="get",outcome="ok"}`)
	assert.Contains(t, s, `seasonfill_s3_bytes_total{op="get"}`)
	assert.Contains(t, s, `seasonfill_s3_requests_total{op="stat",outcome="not_found"}`)
	assert.Contains(t, s, `seasonfill_s3_requests_total{op="get",outcome="timeout"}`)
	assert.Contains(t, s, `seasonfill_s3_response_code_total{op="delete",code="404"}`)
}

func TestClassifyOutcome(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ok", classifyOutcome(nil))
	assert.Equal(t, "not_found", classifyOutcome(fmt.Errorf("x: %w", ErrNotFound)))
	assert.Equal(t, "timeout", classifyOutcome(fmt.Errorf("x: %w", context.DeadlineExceeded)))
	assert.Equal(t, "timeout", classifyOutcome(fmt.Errorf("x: %w", context.Canceled)))
	assert.Equal(t, "error", classifyOutcome(errors.New("other")))
}

func TestStatusFromErr(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 200, statusFromErr(nil))
	assert.Equal(t, 404, statusFromErr(fmt.Errorf("x: %w", minio.ErrorResponse{StatusCode: 404})))
	assert.Equal(t, 0, statusFromErr(errors.New("plain")))
	assert.Equal(t, 0, statusFromErr(context.DeadlineExceeded))
}

// gatedFakeStore lets a test hold Get and/or Put "in flight" (blocked inside
// the inner call) so the read/write semaphore behavior can be exercised.
// *Entered channels signal that the op is inside; *Release channels unblock
// it (close to release). A nil release channel means the op returns
// immediately; ctx cancellation always preempts the block.
type gatedFakeStore struct {
	getInfo ObjectInfo

	getEntered chan struct{}
	getRelease chan struct{}
	putEntered chan struct{}
	putRelease chan struct{}
}

func (f *gatedFakeStore) Get(ctx context.Context, _ string) (io.ReadCloser, ObjectInfo, error) {
	if f.getEntered != nil {
		f.getEntered <- struct{}{}
	}
	if f.getRelease != nil {
		select {
		case <-f.getRelease:
		case <-ctx.Done():
			return nil, ObjectInfo{}, ctx.Err()
		}
	}
	return io.NopCloser(bytes.NewReader([]byte("body"))), f.getInfo, nil
}

func (f *gatedFakeStore) Put(ctx context.Context, _ string, r io.Reader, _ int64, _ string) error {
	_, _ = io.Copy(io.Discard, r)
	if f.putEntered != nil {
		f.putEntered <- struct{}{}
	}
	if f.putRelease != nil {
		select {
		case <-f.putRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (f *gatedFakeStore) Stat(context.Context, string) (ObjectInfo, error) { return f.getInfo, nil }
func (f *gatedFakeStore) Delete(context.Context, string) error             { return nil }
func (f *gatedFakeStore) List(context.Context, string, func(ObjectInfo) error) error {
	return nil
}

// Read/write split: a blocked Put holding the only write slot must NOT
// starve a concurrent Get (independent read semaphore). -race clean.
func TestMeteredStore_WritePutDoesNotStarveReads(t *testing.T) {
	fake := &gatedFakeStore{
		getInfo:    ObjectInfo{Size: 1},
		putEntered: make(chan struct{}, 1),
		putRelease: make(chan struct{}),
		// getRelease nil → Get returns immediately once it holds a read slot.
	}
	m := NewMeteredStore(fake, 2, 1) // write cap 1
	ctx := context.Background()

	putDone := make(chan struct{})
	go func() {
		_ = m.Put(ctx, "k", bytes.NewReader([]byte("x")), 1, "image/jpeg")
		close(putDone)
	}()
	<-fake.putEntered // the single write slot is now occupied

	getReturned := make(chan error, 1)
	go func() {
		rc, _, err := m.Get(ctx, "k")
		if rc != nil {
			_ = rc.Close()
		}
		getReturned <- err
	}()
	select {
	case err := <-getReturned:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Get starved by a blocked Put — read/write semaphores not independent")
	}
	close(fake.putRelease)
	<-putDone
}

// Read-acquire timeout: with the only read slot held by a blocked Get, a
// second Get returns errReadInflightTimeout at ~readAcquireBudget and
// increments the acquire-timeout metric.
func TestMeteredStore_ReadAcquireTimeout(t *testing.T) {
	fake := &gatedFakeStore{
		getInfo:    ObjectInfo{Size: 1},
		getEntered: make(chan struct{}, 1),
		getRelease: make(chan struct{}),
	}
	m := NewMeteredStore(fake, 1, 4) // read cap 1
	ctx := context.Background()

	go func() {
		rc, _, _ := m.Get(ctx, "held")
		if rc != nil {
			_ = rc.Close()
		}
	}()
	<-fake.getEntered // the single read slot is now occupied

	start := time.Now()
	_, _, err := m.Get(ctx, "blocked")
	elapsed := time.Since(start)

	require.ErrorIs(t, err, errReadInflightTimeout)
	require.NotErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, ErrNotSupported)
	assert.GreaterOrEqual(t, elapsed, 400*time.Millisecond)
	assert.Less(t, elapsed, 1500*time.Millisecond)

	body := &bytes.Buffer{}
	observability.WritePrometheus(body)
	assert.Contains(t, body.String(), `seasonfill_s3_acquire_timeout_total{op="get"}`)

	close(fake.getRelease) // let the held Get finish (no goroutine leak)
}

// Write-acquire honors ctx: with the only write slot held, a Put on an
// already-cancelled ctx returns ctx.Err() promptly (does not hang).
func TestMeteredStore_WriteAcquireHonorsCtx(t *testing.T) {
	fake := &gatedFakeStore{
		putEntered: make(chan struct{}, 1),
		putRelease: make(chan struct{}),
	}
	m := NewMeteredStore(fake, 4, 1) // write cap 1
	go func() {
		_ = m.Put(context.Background(), "held", bytes.NewReader([]byte("x")), 1, "")
	}()
	<-fake.putEntered // the single write slot is now occupied

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := m.Put(ctx, "blocked", bytes.NewReader([]byte("y")), 1, "")

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	close(fake.putRelease)
}

// cap<=0 normalizes to the package defaults so a zero-value construction
// never deadlocks on a nil/zero-capacity channel.
func TestNewMeteredStore_ZeroCapsNormalize(t *testing.T) {
	fake := &meterFakeStore{getInfo: ObjectInfo{Size: 1}}
	m := NewMeteredStore(fake, 0, 0)
	ctx := context.Background()

	rc, _, err := m.Get(ctx, "k")
	require.NoError(t, err)
	_ = rc.Close()
	require.NoError(t, m.Put(ctx, "k", bytes.NewReader([]byte("x")), 1, ""))

	ms, ok := m.(*meteredStore)
	require.True(t, ok)
	assert.Equal(t, defaultReadInflight, cap(ms.readSem))
	assert.Equal(t, defaultWriteInflight, cap(ms.writeSem))
}

// Story 1111 F-02 — the read in-flight slot is held until the returned body is
// Closed, not until Get returns. Proof: with read cap 1, a second Get cannot
// acquire while the first rc is still open; after Close it can.
func TestMeteredStore_Get_HoldsReadSlotUntilClose(t *testing.T) {
	fake := &meterFakeStore{getInfo: ObjectInfo{Size: 4}}
	m := NewMeteredStore(fake, 1, 4) // read cap 1
	ctx := context.Background()

	// First Get takes the only read slot; body NOT yet closed.
	rc1, _, err := m.Get(ctx, "k")
	require.NoError(t, err)
	require.NotNil(t, rc1)

	// Second Get on a short-deadline ctx must fail to acquire — the slot is
	// still held by the un-Closed rc1 (proves release is deferred to Close, not
	// Get-return). The deadline (100ms) fires before readAcquireBudget (500ms).
	dctx, dcancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer dcancel()
	_, _, err2 := m.Get(dctx, "k")
	require.Error(t, err2)
	require.ErrorIs(t, err2, context.DeadlineExceeded)

	// Drain + Close rc1 → releases the read slot.
	_, _ = io.ReadAll(rc1)
	require.NoError(t, rc1.Close())

	// Double Close is a safe no-op (sync.Once) — must not over-release the sem.
	require.NoError(t, rc1.Close())

	// Third Get now acquires immediately (slot was freed exactly once).
	rc3, _, err3 := m.Get(ctx, "k")
	require.NoError(t, err3)
	_ = rc3.Close()
}

// Story 1111 F-02 — on the Get error path (inner fails, rc nil) the slot is
// released immediately so it is never leaked.
func TestMeteredStore_Get_ErrorPathReleasesSlot(t *testing.T) {
	sentinel := errors.New("inner get boom")
	fake := &meterFakeStore{getErr: sentinel}
	m := NewMeteredStore(fake, 1, 4) // read cap 1
	ctx := context.Background()

	_, _, err := m.Get(ctx, "k")
	require.ErrorIs(t, err, sentinel)

	// The slot must be free — a follow-up Get acquires without timing out.
	dctx, dcancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer dcancel()
	rc, _, err2 := m.Get(dctx, "k2") // this fake still returns the sentinel, but acquire must succeed first
	if rc != nil {
		_ = rc.Close()
	}
	require.ErrorIs(t, err2, sentinel) // reached inner.Get (acquired) → same error, NOT a deadline
}
