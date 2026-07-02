package seriesdetail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

// fakeSkeletonComposer satisfies seriesdetail.SkeletonComposerPort.
type fakeSkeletonComposer struct {
	gotSeriesID domain.SeriesID
	gotLang     values.LanguageTag
	resp        seriesdetail.SkeletonDTO
	err         error
}

func (f *fakeSkeletonComposer) Compose(_ context.Context, id domain.SeriesID, lang values.LanguageTag) (seriesdetail.SkeletonDTO, error) {
	f.gotSeriesID = id
	f.gotLang = lang
	return f.resp, f.err
}

func TestGlobalComposerUseCase_DelegatesToSkeleton(t *testing.T) {
	sk := &fakeSkeletonComposer{resp: seriesdetail.SkeletonDTO{
		SeriesID:           140,
		InLibraryInstances: []string{"homelab"},
	}}
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{Skeleton: sk})
	require.NoError(t, err)

	dto, err := uc.Get(context.Background(), 140, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, domain.SeriesID(140), dto.SeriesID)
	assert.Equal(t, []string{"homelab"}, dto.InLibraryInstances)
	assert.Equal(t, domain.SeriesID(140), sk.gotSeriesID)
	assert.Equal(t, "ru-RU", sk.gotLang.Value())
}

func TestGlobalComposerUseCase_EmptyLangDefaultsEnUS(t *testing.T) {
	sk := &fakeSkeletonComposer{}
	uc, _ := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{Skeleton: sk})
	_, err := uc.Get(context.Background(), 140, "")
	require.NoError(t, err)
	assert.Equal(t, "en-US", sk.gotLang.Value())
	// A non xx-XX tag also defaults.
	_, _ = uc.Get(context.Background(), 140, "garbage")
	assert.Equal(t, "en-US", sk.gotLang.Value())
}

func TestGlobalComposerUseCase_InvalidSeriesID(t *testing.T) {
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{Skeleton: &fakeSkeletonComposer{}})
	require.NoError(t, err)
	_, err = uc.Get(context.Background(), 0, "en-US")
	assert.ErrorIs(t, err, ports.ErrNotFound)
	_, err = uc.Get(context.Background(), -5, "en-US")
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGlobalComposerUseCase_ComposeError_Propagates(t *testing.T) {
	wantErr := errors.New("compose boom")
	uc, _ := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{Skeleton: &fakeSkeletonComposer{err: wantErr}})
	_, err := uc.Get(context.Background(), 140, "en-US")
	assert.ErrorIs(t, err, wantErr)
}

func TestNewGlobalComposerUseCase_NilSkeleton(t *testing.T) {
	_, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{})
	require.Error(t, err)
}
