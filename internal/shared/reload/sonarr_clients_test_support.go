//go:build test_support

package reload

import (
	"context"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// FakeSonarrClient is an exported stub of ports.SonarrClient for
// cross-package use in 027d-3's E2E tests. Every method returns the
// zero value of its declared return type + nil error.
type FakeSonarrClient struct {
	InstanceName domain.InstanceName
}

func (f *FakeSonarrClient) Name() string { return string(f.InstanceName) }

func (f *FakeSonarrClient) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (f *FakeSonarrClient) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *FakeSonarrClient) ListSeriesCache(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *FakeSonarrClient) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *FakeSonarrClient) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (f *FakeSonarrClient) ListEpisodesBySeries(_ context.Context, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *FakeSonarrClient) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *FakeSonarrClient) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *FakeSonarrClient) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *FakeSonarrClient) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *FakeSonarrClient) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *FakeSonarrClient) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *FakeSonarrClient) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}

var _ ports.SonarrClient = (*FakeSonarrClient)(nil)
