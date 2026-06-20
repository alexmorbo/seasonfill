package webhook_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	appwebhook "github.com/alexmorbo/seasonfill/internal/catalog/app/webhook"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- minimal fakes -------------------------------------------------------

type fakeSonarr struct {
	parseFn func(ctx context.Context, title string) (ports.ParseResult, error)
}

func (f *fakeSonarr) ParseRelease(ctx context.Context, title string) (ports.ParseResult, error) {
	if f.parseFn == nil {
		return ports.ParseResult{}, nil
	}
	return f.parseFn(ctx, title)
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
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
func (f *fakeSonarr) Name() string { return "" }

// only methods we need; the rest of ports.SonarrClient is satisfied via embedding
// in the helper below if more methods land. For now the use case only calls ParseRelease.

type fakeGrabs struct {
	rec         grab.Record
	updateCalls int
	updateErr   error
	lastParsed  *grab.Parsed
}

func (f *fakeGrabs) Create(_ context.Context, _ grab.Record) error { return nil }
func (f *fakeGrabs) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	return nil, nil, nil
}
func (f *fakeGrabs) MatchLatest(_ context.Context, _ ports.MatchKey) (grab.Record, error) {
	return f.rec, nil
}
func (f *fakeGrabs) UpdateStatus(_ context.Context, _ uuid.UUID, _ grab.Status, _ string) error {
	return nil
}
func (f *fakeGrabs) UpdateTorrentHash(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeGrabs) FindLatestSuccessByHash(_ context.Context, _ string) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}
func (f *fakeGrabs) CreateReplay(_ context.Context, _ grab.Record, _ uuid.UUID) error { return nil }
func (f *fakeGrabs) SetReplayOfID(_ context.Context, _ uuid.UUID, _ uuid.UUID) error  { return nil }
func (f *fakeGrabs) ListUnparsedSince(_ context.Context, _ time.Time, _ int) ([]grab.Record, error) {
	return nil, nil
}
func (f *fakeGrabs) UpdateParsed(_ context.Context, _ uuid.UUID, p *grab.Parsed, _ time.Time) error {
	f.updateCalls++
	f.lastParsed = p
	return f.updateErr
}
func (f *fakeGrabs) UpdateSizeBytes(_ context.Context, _ uuid.UUID, _ int64) error { return nil }
func (f *fakeGrabs) CountImportedEpisodes(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) (int, error) {
	return 0, nil
}
func (f *fakeGrabs) GetByID(_ context.Context, _ uuid.UUID) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}
func (f *fakeGrabs) CountReplaysSince(_ context.Context, _ domain.InstanceName, _ time.Time) (int, error) {
	return 0, nil
}
func (f *fakeGrabs) CountReplaysAll(_ context.Context, _ domain.InstanceName) (int, error) {
	return 0, nil
}
func (f *fakeGrabs) ListReplaysOf(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return nil, nil
}

// --- tests ---------------------------------------------------------------

func TestRunParseOnGrab_HappyPath_PersistsParsed(t *testing.T) {
	t.Parallel()
	grabs := &fakeGrabs{rec: grab.Record{ID: uuid.New(), InstanceName: "alpha"}}
	sc := &fakeSonarr{parseFn: func(_ context.Context, _ string) (ports.ParseResult, error) {
		return ports.ParseResult{Quality: "WEBDL-2160p", Source: "WEB-DL", Resolution: 2160,
			Languages: []string{"Russian"}, ReleaseGroup: "NTb"}, nil
	}}
	uc := appwebhook.New(appwebhook.Deps{
		Grabs:           grabs,
		SonarrClientFor: func(string) (ports.SonarrClient, bool) { return sc, true },
		InstanceFor: func(string) (runtime.InstanceSnapshot, bool) {
			return runtime.InstanceSnapshot{Name: "alpha", ParseOnGrabEnabled: true}, true
		},
	})
	// 40-char hex DownloadID so handleGrabbed accepts it.
	evt := domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "alpha",
		DownloadID:   "abcdef0123456789abcdef0123456789abcdef01",
		ReleaseTitle: "Foundation.S02.2160p.WEB-DL.HEVC",
	}
	if err := uc.Process(context.Background(), evt); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if grabs.updateCalls != 1 || grabs.lastParsed == nil || grabs.lastParsed.Quality != "WEBDL-2160p" {
		t.Fatalf("parsed not persisted: calls=%d parsed=%+v", grabs.updateCalls, grabs.lastParsed)
	}
}

func TestRunParseOnGrab_Disabled_NoCall(t *testing.T) {
	t.Parallel()
	grabs := &fakeGrabs{rec: grab.Record{ID: uuid.New(), InstanceName: "alpha"}}
	sc := &fakeSonarr{parseFn: func(_ context.Context, _ string) (ports.ParseResult, error) {
		t.Fatal("ParseRelease must not be called when disabled")
		return ports.ParseResult{}, nil
	}}
	uc := appwebhook.New(appwebhook.Deps{
		Grabs:           grabs,
		SonarrClientFor: func(string) (ports.SonarrClient, bool) { return sc, true },
		InstanceFor: func(string) (runtime.InstanceSnapshot, bool) {
			return runtime.InstanceSnapshot{Name: "alpha", ParseOnGrabEnabled: false}, true
		},
	})
	evt := domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "alpha",
		DownloadID:   "abcdef0123456789abcdef0123456789abcdef01",
		ReleaseTitle: "any",
	}
	if err := uc.Process(context.Background(), evt); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if grabs.updateCalls != 0 {
		t.Fatalf("UpdateParsed must not run when disabled (calls=%d)", grabs.updateCalls)
	}
}

func TestRunParseOnGrab_SonarrErr_GrabStillSucceeds(t *testing.T) {
	t.Parallel()
	grabs := &fakeGrabs{rec: grab.Record{ID: uuid.New(), InstanceName: "alpha"}}
	sc := &fakeSonarr{parseFn: func(_ context.Context, _ string) (ports.ParseResult, error) {
		return ports.ParseResult{}, errors.New("503")
	}}
	uc := appwebhook.New(appwebhook.Deps{
		Grabs:           grabs,
		SonarrClientFor: func(string) (ports.SonarrClient, bool) { return sc, true },
		InstanceFor: func(string) (runtime.InstanceSnapshot, bool) {
			return runtime.InstanceSnapshot{Name: "alpha", ParseOnGrabEnabled: true}, true
		},
	})
	evt := domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "alpha",
		DownloadID:   "abcdef0123456789abcdef0123456789abcdef01",
		ReleaseTitle: "any",
	}
	if err := uc.Process(context.Background(), evt); err != nil {
		t.Fatalf("Process must succeed even when parse fails: %v", err)
	}
	if grabs.updateCalls != 0 {
		t.Fatalf("UpdateParsed must not run when parse errored")
	}
}
