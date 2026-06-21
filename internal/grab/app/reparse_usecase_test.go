package grab

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedDomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// reparseFakeGrabs is the minimal ReparseGrabs implementation the
// tests use. Records every UpdateParsed under a mutex so concurrent
// access (none today, but defensive) stays race-clean.
type reparseFakeGrabs struct {
	unparsed  []domaingrab.Record
	updateErr error

	mu      sync.Mutex
	updated map[uuid.UUID]*domaingrab.Parsed
}

func newReparseFakeGrabs(rows []domaingrab.Record) *reparseFakeGrabs {
	return &reparseFakeGrabs{
		unparsed: rows,
		updated:  map[uuid.UUID]*domaingrab.Parsed{},
	}
}

func (f *reparseFakeGrabs) ListUnparsedSince(_ context.Context, _ time.Time, _ int) ([]domaingrab.Record, error) {
	return f.unparsed, nil
}

func (f *reparseFakeGrabs) UpdateParsed(_ context.Context, id uuid.UUID, p *domaingrab.Parsed, _ time.Time) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated[id] = p
	return nil
}

// reparseFakeSonarr is the minimal ReparseSonarr stub. ParseRelease
// looks up the title in a fixed map; absent titles return zero-value
// (no-op) results matching the live client's tolerance for un-parseable
// titles.
type reparseFakeSonarr struct {
	mu      sync.Mutex
	titles  []string
	results map[string]parseResultOrErr
}

type parseResultOrErr struct {
	res ports.ParseResult
	err error
}

func newReparseFakeSonarr(results map[string]parseResultOrErr) *reparseFakeSonarr {
	return &reparseFakeSonarr{results: results}
}

func (f *reparseFakeSonarr) ParseRelease(_ context.Context, title string) (ports.ParseResult, error) {
	f.mu.Lock()
	f.titles = append(f.titles, title)
	f.mu.Unlock()
	r, ok := f.results[title]
	if !ok {
		return ports.ParseResult{}, nil
	}
	return r.res, r.err
}

func sampleUnparsed(id uuid.UUID, instance sharedDomain.InstanceName, title string, status domaingrab.Status) domaingrab.Record {
	return domaingrab.Record{
		ID:           id,
		InstanceName: instance,
		ReleaseTitle: title,
		Status:       status,
	}
}

func TestReparseUseCase_SkipsTerminalRows(t *testing.T) {
	t.Parallel()
	pendingID := uuid.New()
	importedID := uuid.New()
	rows := []domaingrab.Record{
		sampleUnparsed(pendingID, "alpha", "Foundation.S02.1080p.WEB-DL", domaingrab.StatusGrabbed),
		sampleUnparsed(importedID, "alpha", "Foundation.S02.2160p.WEB-DL", domaingrab.StatusImported),
	}
	grabs := newReparseFakeGrabs(rows)
	sonarr := newReparseFakeSonarr(map[string]parseResultOrErr{
		"Foundation.S02.1080p.WEB-DL": {res: ports.ParseResult{Quality: "WEBDL-1080p", Resolution: 1080}},
	})
	uc := NewReparseUseCase(grabs, sonarr, slog.Default())

	processed, err := uc.ReplayInstance(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, 1, processed)

	grabs.mu.Lock()
	defer grabs.mu.Unlock()
	require.Contains(t, grabs.updated, pendingID, "pending row must be parsed")
	require.NotContains(t, grabs.updated, importedID, "imported row must be skipped")
}

func TestReparseUseCase_FiltersByInstance(t *testing.T) {
	t.Parallel()
	alphaID := uuid.New()
	betaID := uuid.New()
	rows := []domaingrab.Record{
		sampleUnparsed(alphaID, "alpha", "X.S01.PACK", domaingrab.StatusGrabbed),
		sampleUnparsed(betaID, "beta", "Y.S01.PACK", domaingrab.StatusGrabbed),
	}
	grabs := newReparseFakeGrabs(rows)
	sonarr := newReparseFakeSonarr(map[string]parseResultOrErr{
		"X.S01.PACK": {res: ports.ParseResult{Quality: "WEBDL-1080p"}},
	})
	uc := NewReparseUseCase(grabs, sonarr, slog.Default())

	processed, err := uc.ReplayInstance(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, 1, processed)

	grabs.mu.Lock()
	defer grabs.mu.Unlock()
	require.Contains(t, grabs.updated, alphaID)
	require.NotContains(t, grabs.updated, betaID, "cross-instance row must not be parsed")
}

func TestReparseUseCase_NonFatalSonarrError_ContinuesLoop(t *testing.T) {
	t.Parallel()
	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()
	rows := []domaingrab.Record{
		sampleUnparsed(id1, "alpha", "OK.S01.PACK", domaingrab.StatusGrabbed),
		sampleUnparsed(id2, "alpha", "FAIL.S01.PACK", domaingrab.StatusGrabbed),
		sampleUnparsed(id3, "alpha", "AFTER.S01.PACK", domaingrab.StatusGrabbed),
	}
	grabs := newReparseFakeGrabs(rows)
	sonarr := newReparseFakeSonarr(map[string]parseResultOrErr{
		"OK.S01.PACK":    {res: ports.ParseResult{Quality: "WEBDL-1080p"}},
		"FAIL.S01.PACK":  {err: errors.New("sonarr 502 bad gateway")},
		"AFTER.S01.PACK": {res: ports.ParseResult{Quality: "WEBDL-1080p"}},
	})
	uc := NewReparseUseCase(grabs, sonarr, slog.Default())

	processed, err := uc.ReplayInstance(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, 2, processed,
		"3 candidates – 1 sonarr 502 = 2 persisted")
	grabs.mu.Lock()
	defer grabs.mu.Unlock()
	require.Contains(t, grabs.updated, id1)
	require.NotContains(t, grabs.updated, id2,
		"row whose Sonarr ParseRelease returned 502 stays parsed_at=NULL")
	require.Contains(t, grabs.updated, id3)
}

func TestReparseUseCase_EmptyTitleSkipped(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	rows := []domaingrab.Record{
		sampleUnparsed(id1, "alpha", "", domaingrab.StatusGrabbed),
	}
	grabs := newReparseFakeGrabs(rows)
	sonarr := newReparseFakeSonarr(nil)
	uc := NewReparseUseCase(grabs, sonarr, slog.Default())

	processed, err := uc.ReplayInstance(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, 0, processed)
	assert.Empty(t, sonarr.titles, "empty title must not hit Sonarr")
}

func TestReparseUseCase_PersistFailurePropagates(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	rows := []domaingrab.Record{
		sampleUnparsed(id1, "alpha", "OK.S01.PACK", domaingrab.StatusGrabbed),
	}
	grabs := newReparseFakeGrabs(rows)
	grabs.updateErr = errors.New("db unavailable")
	sonarr := newReparseFakeSonarr(map[string]parseResultOrErr{
		"OK.S01.PACK": {res: ports.ParseResult{Quality: "WEBDL-1080p"}},
	})
	uc := NewReparseUseCase(grabs, sonarr, slog.Default())

	processed, err := uc.ReplayInstance(context.Background(), "alpha")
	require.Error(t, err)
	assert.Equal(t, 0, processed)
	assert.Contains(t, err.Error(), "db unavailable")
}
