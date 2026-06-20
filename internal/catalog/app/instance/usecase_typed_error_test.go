package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// typedNFRepo is a minimal SonarrInstanceRepository whose GetByName
// returns the production typed-chain shape (typed NF joined with
// ports.ErrNotFound) so the test can assert the use case preserves
// it through Get.
type typedNFRepo struct {
	name string
}

func (r *typedNFRepo) GetByName(_ context.Context, name string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	return runtime.InstanceSnapshot{}, errors.Join(
		&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
		ports.ErrNotFound,
	)
}
func (r *typedNFRepo) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	return nil, nil
}
func (r *typedNFRepo) Create(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	return 0, nil
}
func (r *typedNFRepo) UpdateWithOptions(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool, _ *time.Time) error {
	return nil
}
func (r *typedNFRepo) Delete(_ context.Context, _ string) error { return nil }
func (r *typedNFRepo) GetUpdatedAt(_ context.Context, name string) (time.Time, error) {
	return time.Time{}, errors.Join(
		&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
		ports.ErrNotFound,
	)
}

// TestCreate_DupName_NoTypedChain ensures the duplicate-name error
// path does NOT synthesize a typed InstanceNotFoundError chain — the
// "row already exists" surface is a distinct error from "row not
// found" and must not leak the typed NF identity.
func TestCreate_DupName_NoTypedChain(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	err := uc.Create(context.Background(), validSnap("alpha"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateName)
	var typed *sharedErrors.InstanceNotFoundError
	assert.False(t, errors.As(err, &typed),
		"duplicate-name error must not synthesize a typed NF chain")
}

// TestGet_TypedChain_PreservesInstanceNF asserts that when the
// underlying repo returns the typed InstanceNotFoundError joined with
// ports.ErrNotFound (production shape), the use case returns it
// unchanged so HTTP middleware dispatches instance_not_found.
func TestGet_TypedChain_PreservesInstanceNF(t *testing.T) {
	t.Parallel()
	// Use a dedicated typed-fake that returns the production-shape
	// (typed joined with ports.ErrNotFound) on GetByName so we can
	// assert the use case is transparent across the wrap layer.
	typedRepo := &typedNFRepo{name: "ghost"}
	customUC := New(typedRepo, &fakeRuntimeRepo{}, nil, nil, nil)
	_, _, err := customUC.Get(context.Background(), "ghost")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound),
		"errors.Is(ports.ErrNotFound) must keep working")
	var typed *sharedErrors.InstanceNotFoundError
	require.True(t, errors.As(err, &typed),
		"typed InstanceNotFoundError chain must survive the use-case wrap")
	assert.Equal(t, domain.InstanceName("ghost"), typed.Name)
}
