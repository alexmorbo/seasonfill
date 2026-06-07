package reload

import (
	"context"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

// fakeSonarrClient stubs ports.SonarrClient for tests in this package.
// Every method returns the zero value of its declared return type + nil error.
type fakeSonarrClient struct {
	fakeClient
}

func (f *fakeSonarrClient) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (f *fakeSonarrClient) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarrClient) ListSeriesCache(_ context.Context, _ string) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSonarrClient) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarrClient) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarrClient) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarrClient) ListEpisodeFilesBySeason(_ context.Context, _, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (f *fakeSonarrClient) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarrClient) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarrClient) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarrClient) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarrClient) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarrClient) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}

var _ ports.SonarrClient = (*fakeSonarrClient)(nil)
