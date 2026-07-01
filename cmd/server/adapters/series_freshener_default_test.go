package adapters

import (
	"context"
	"testing"
	"time"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

// stubProbe is a minimal freshener.Probe used only by the default-config
// guard test below. Kept internal to the package so we can inspect the
// unexported cfg field without exporting an accessor.
type stubProbe struct{}

func (stubProbe) IsStale(context.Context, domain.SeriesID, values.LanguageTag, []int) ([]freshener.SectionVerdict, error) {
	return nil, nil
}

type stubAsyncEnricher struct{}

func (stubAsyncEnricher) EnqueueIfStale(domain.SeriesID, catalogseries.Hydration) {}

// TestSeriesFreshenerHolder_DefaultSyncTimeout locks in the Story 567
// default: when caller omits SyncTimeout, the guard applies 5s (up from
// the pre-567 3s). Direct-field assertion is possible because this test
// lives in package adapters (not adapters_test).
func TestSeriesFreshenerHolder_DefaultSyncTimeout(t *testing.T) {
	t.Parallel()
	h, err := NewSeriesFreshenerHolder(SeriesFreshenerConfig{
		Probe:         stubProbe{},
		AsyncEnricher: stubAsyncEnricher{},
	})
	if err != nil {
		t.Fatalf("NewSeriesFreshenerHolder: %v", err)
	}
	if got, want := h.cfg.SyncTimeout, 5*time.Second; got != want {
		t.Fatalf("default SyncTimeout: got %s, want %s", got, want)
	}
}
