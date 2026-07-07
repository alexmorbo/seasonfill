package mediastore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

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
	m := NewMeteredStore(fake)
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
	m := NewMeteredStore(fake)
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
	okGet := NewMeteredStore(&meterFakeStore{getInfo: ObjectInfo{Size: 1234}})
	rc, _, err := okGet.Get(ctx, "k")
	require.NoError(t, err)
	_ = rc.Close()

	// not_found stat → records stat/not_found.
	nfStat := NewMeteredStore(&meterFakeStore{statErr: fmt.Errorf("wrap: %w", ErrNotFound)})
	_, _ = nfStat.Stat(ctx, "missing")

	// timeout get → records get/timeout (context.DeadlineExceeded chain).
	toGet := NewMeteredStore(&meterFakeStore{getErr: fmt.Errorf("wrap: %w", context.DeadlineExceeded)})
	_, _, _ = toGet.Get(ctx, "slow")

	// minio 404 delete → response code 404 recorded.
	me := minio.ErrorResponse{StatusCode: 404, Code: "NoSuchKey"}
	codeDel := NewMeteredStore(&meterFakeStore{delErr: fmt.Errorf("wrap: %w", me)})
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
