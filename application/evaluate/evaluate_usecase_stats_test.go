package evaluate

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

// recordingDecisionRepo captures every Save call for assertion.
type recordingDecisionRepo struct {
	saved []decision.Decision
}

func (r *recordingDecisionRepo) Save(_ context.Context, d decision.Decision) error {
	r.saved = append(r.saved, d)
	return nil
}
func (r *recordingDecisionRepo) GetByID(context.Context, uuid.UUID) (decision.Decision, error) {
	return decision.Decision{}, ports.ErrNotFound
}
func (r *recordingDecisionRepo) List(context.Context, ports.DecisionFilter, ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	return nil, nil, nil
}
func (r *recordingDecisionRepo) UpdateSupersededBy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (r *recordingDecisionRepo) ClearSupersededBy(context.Context, uuid.UUID) error { return nil }

// stubGrabRepo returns a configurable CountImportedEpisodes value and
// records the (instance, series, season) tuple it was queried with.
type stubGrabRepo struct {
	count    int
	err      error
	calledFn func(string, int, int)
}

func (s *stubGrabRepo) CountImportedEpisodes(_ context.Context, inst string, sid, sn int) (int, error) {
	if s.calledFn != nil {
		s.calledFn(inst, sid, sn)
	}
	return s.count, s.err
}

// Minimal stub methods to satisfy interface without full impl
func (s *stubGrabRepo) Create(context.Context, grab.Record) error { return nil }
func (s *stubGrabRepo) List(context.Context, ports.GrabFilter, ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	return nil, nil, nil
}
func (s *stubGrabRepo) MatchLatest(context.Context, ports.MatchKey) (grab.Record, error) {
	return grab.Record{}, nil
}
func (s *stubGrabRepo) UpdateStatus(context.Context, uuid.UUID, grab.Status, string) error {
	return nil
}
func (s *stubGrabRepo) UpdateTorrentHash(context.Context, uuid.UUID, string) error { return nil }
func (s *stubGrabRepo) FindLatestSuccessByHash(context.Context, string) (grab.Record, error) {
	return grab.Record{}, nil
}
func (s *stubGrabRepo) CreateReplay(context.Context, grab.Record, uuid.UUID) error { return nil }
func (s *stubGrabRepo) SetReplayOfID(context.Context, uuid.UUID, uuid.UUID) error  { return nil }
func (s *stubGrabRepo) ListReplaysOf(context.Context, []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return nil, nil
}
func (s *stubGrabRepo) UpdateSizeBytes(context.Context, uuid.UUID, int64) error { return nil }
func (s *stubGrabRepo) GetByID(context.Context, uuid.UUID) (grab.Record, error) {
	return grab.Record{}, nil
}
func (s *stubGrabRepo) CountReplaysSince(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
func (s *stubGrabRepo) CountReplaysAll(context.Context, string) (int, error) { return 0, nil }

func newLoggerDiscard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestExecute_PopulatesSeasonStats_HappyPath asserts that the four new
// counter fields land on every persisted Decision row even on skip
// branches (when full season already exists).
func TestExecute_PopulatesSeasonStats_HappyPath(t *testing.T) {
	decisions := &recordingDecisionRepo{}
	grabs := &stubGrabRepo{count: 5}
	uc := NewPerInstanceUseCase(decisions, newLoggerDiscard()).WithGrabRepository(grabs)

	s := series.Series{
		ID: 101, Title: "Test", Type: series.SeriesTypeStandard, Monitored: true,
	}
	// All 10 episodes already exist (complete season)
	season := series.Season{
		Number: 1, Monitored: true,
		Statistics: series.Statistics{Total: 10, Aired: 8, EpisodeFileCount: 10, EpisodeCount: 10},
		Episodes:   make([]series.Episode, 10),
	}
	for i := 0; i < 10; i++ {
		season.Episodes[i] = series.Episode{
			Number: i + 1, SeasonNumber: 1, Monitored: true, HasFile: true,
		}
	}

	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(),
		Instance:  "homelab",
		Series:    s,
		Season:    season,
		Now:       time.Now().UTC(),
	})
	require.NoError(t, err)
	require.Len(t, decisions.saved, 1)
	got := decisions.saved[0]
	assert.Equal(t, 10, got.TotalEpisodes)
	assert.Equal(t, 8, got.AiredEpisodes)
	assert.Equal(t, 10, got.ExistingEpisodes)
	assert.Equal(t, 5, got.GrabbedEpisodes)
	assert.Equal(t, "homelab", d.InstanceName)
	assert.Equal(t, decision.OutcomeSkip, got.Outcome)
	assert.Equal(t, decision.ReasonSkipNoMissing, got.Reason)
}

// TestExecute_PopulatesSeasonStats_OnEarlySkip asserts that the stats
// land even on skip branches that exit before SearchReleases — the
// snapshot is populated unconditionally before any short-circuit.
func TestExecute_PopulatesSeasonStats_OnEarlySkip(t *testing.T) {
	decisions := &recordingDecisionRepo{}
	uc := NewPerInstanceUseCase(decisions, newLoggerDiscard())
	s := series.Series{ID: 200, Title: "Specials Only", Type: series.SeriesTypeStandard, Monitored: true}
	season := series.Season{
		Number: 0, Monitored: true,
		Statistics: series.Statistics{Total: 5, Aired: 5, EpisodeFileCount: 2},
	}
	_, err := uc.Execute(context.Background(), Input{
		ScanRunID:    uuid.New(),
		Instance:     "homelab",
		Series:       s,
		Season:       season,
		SkipSpecials: true,
		Now:          time.Now().UTC(),
	})
	require.NoError(t, err)
	require.Len(t, decisions.saved, 1)
	got := decisions.saved[0]
	assert.Equal(t, decision.OutcomeSkip, got.Outcome)
	assert.Equal(t, decision.ReasonSkipSpecials, got.Reason)
	assert.Equal(t, 5, got.TotalEpisodes)
	assert.Equal(t, 5, got.AiredEpisodes)
	assert.Equal(t, 2, got.ExistingEpisodes)
}

// TestExecute_GrabRepoUnwired_GrabbedEpisodesZero asserts the
// optional-port pattern: without WithGrabRepository, GrabbedEpisodes
// remains 0 and no panic occurs.
func TestExecute_GrabRepoUnwired_GrabbedEpisodesZero(t *testing.T) {
	decisions := &recordingDecisionRepo{}
	uc := NewPerInstanceUseCase(decisions, newLoggerDiscard())
	s := series.Series{ID: 300, Title: "T", Type: series.SeriesTypeStandard, Monitored: true}
	season := series.Season{
		Number: 1, Monitored: true,
		Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 10},
	}
	_, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "homelab",
		Series: s, Season: season, Now: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.Len(t, decisions.saved, 1)
	assert.Equal(t, 0, decisions.saved[0].GrabbedEpisodes)
}

// TestExecute_GrabRepoError_LogsAndContinues asserts that a flaky DB
// read for GrabbedEpisodes is logged and swallowed (Decision still
// persists with GrabbedEpisodes=0).
func TestExecute_GrabRepoError_LogsAndContinues(t *testing.T) {
	decisions := &recordingDecisionRepo{}
	grabs := &stubGrabRepo{err: errInjected}
	uc := NewPerInstanceUseCase(decisions, newLoggerDiscard()).WithGrabRepository(grabs)
	s := series.Series{ID: 400, Title: "T", Type: series.SeriesTypeStandard, Monitored: true}
	season := series.Season{
		Number: 1, Monitored: true,
		Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 10},
	}
	_, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "homelab",
		Series: s, Season: season, Now: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.Len(t, decisions.saved, 1)
	assert.Equal(t, 0, decisions.saved[0].GrabbedEpisodes)
}

// errInjected is a sentinel for the stubGrabRepo error path. Declared
// here (not in a test_helpers file) because no other test consumes it.
var errInjected = errInjectedT{}

type errInjectedT struct{}

func (errInjectedT) Error() string { return "injected db failure" }

var _ release.Release // silence unused import on test-only files
