package mediastore

import (
	"context"
	"io"
)

// nullStore is returned for Mode == ModeOff. Every operation returns
// ErrNotSupported so callers can branch on a typed sentinel and fall
// back to the legacy hotlink path instead of treating "no store" as a
// hard failure.
type nullStore struct{}

func newNullStore() Store { return nullStore{} }

func (nullStore) Get(_ context.Context, _ string) (io.ReadCloser, ObjectInfo, error) {
	return nil, ObjectInfo{}, ErrNotSupported
}

func (nullStore) Put(_ context.Context, _ string, _ io.Reader, _ int64, _ string) error {
	return ErrNotSupported
}

func (nullStore) Stat(_ context.Context, _ string) (ObjectInfo, error) {
	return ObjectInfo{}, ErrNotSupported
}

func (nullStore) Delete(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (nullStore) List(_ context.Context, _ string, _ func(ObjectInfo) error) error {
	return ErrNotSupported
}
