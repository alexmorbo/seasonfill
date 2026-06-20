package handlers

import (
	"context"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeSonarr is the shared test double satisfying ports.SonarrClient.
// Extracted from health_test.go (story 430 A-1-4) so instances_test.go,
// grab_test.go, and any other in-package test can continue to embed it
// after the health handler moved into internal/admin/rest. The admin
// rest test tree carries its own copy under that path — duplication
// is deliberate: the two test packages are independent compilation
// units and the fake has no behaviour worth centralising.
type fakeSonarr struct {
	name string
	err  error
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	if f.err != nil {
		return ports.SystemStatus{}, f.err
	}
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarr) ListSeriesCache(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSonarr) GetSeries(_ context.Context, _ domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (f *fakeSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ domain.SonarrSeriesID) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFilesBySeason(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (f *fakeSonarr) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (f *fakeSonarr) Name() string { return f.name }
