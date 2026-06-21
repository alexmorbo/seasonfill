package errors_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// newSeriesID returns a non-shared series id for use as a test sentinel.
// Per D-0 rule, tests must not share fixed ids across cases.
func newSeriesID() domain.SeriesID { return domain.SeriesID(rand.Int64N(1_000_000) + 1) }

func newTMDBID() int { return int(rand.Int32N(1_000_000) + 1) }

func newIMDBID() string { return fmt.Sprintf("tt%07d", rand.Int32N(9_999_999)+1) }

func newSonarrSeriesID() domain.SonarrSeriesID {
	return domain.SonarrSeriesID(rand.Int32N(1_000_000) + 1)
}

func newEpisodeID() domain.EpisodeID { return domain.EpisodeID(rand.Int64N(1_000_000) + 1) }

func newScanRunID() uuid.UUID { return uuid.New() }

func newDecisionID() uuid.UUID { return uuid.New() }

func newInstanceID() uint { return uint(rand.Uint32N(1_000_000) + 1) }

func newWBID() uint { return uint(rand.Uint32N(1_000_000) + 1) }

func newGrabID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		rand.Uint32(), rand.Uint32N(0xffff), rand.Uint32N(0xffff),
		rand.Uint32N(0xffff), rand.Uint64N(0xffffffffffff))
}

func TestIsRetriable_PerType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retriable bool
	}{
		{"SeriesNotFoundError", &sharedErrors.SeriesNotFoundError{ID: newSeriesID()}, false},
		{"SeriesCanonicalLoadError", &sharedErrors.SeriesCanonicalLoadError{ID: newSeriesID(), Cause: errors.New("boom")}, true},
		{"SonarrUnreachableError", &sharedErrors.SonarrUnreachableError{Instance: "main", Cause: errors.New("dial tcp")}, true},
		{"SonarrInstanceInvalidError", &sharedErrors.SonarrInstanceInvalidError{Instance: "ghost"}, false},
		{"TMDBRateLimitedError", &sharedErrors.TMDBRateLimitedError{RetryAfter: 5 * time.Second}, true},
		{"TMDBAuthError", &sharedErrors.TMDBAuthError{}, false},
		{"TMDBNotFoundError", &sharedErrors.TMDBNotFoundError{ID: newTMDBID()}, false},
		{"OMDbQuotaExhaustedError", &sharedErrors.OMDbQuotaExhaustedError{ResetAt: time.Now().Add(time.Hour)}, false},
		{"OMDbNotFoundError", &sharedErrors.OMDbNotFoundError{IMDBID: newIMDBID()}, false},
		{"OMDbAuthError", &sharedErrors.OMDbAuthError{}, false},
		{"ScanFailedError", &sharedErrors.ScanFailedError{Cause: errors.New("disk full")}, true},
		{"ScanInProgressError", &sharedErrors.ScanInProgressError{}, false},
		{"SeriesCacheNotFoundError", &sharedErrors.SeriesCacheNotFoundError{InstanceName: "main", SonarrSeriesID: newSonarrSeriesID()}, false},
		{"EpisodeNotFoundError", &sharedErrors.EpisodeNotFoundError{ID: newEpisodeID()}, false},
		{"SeasonNotFoundError", &sharedErrors.SeasonNotFoundError{InstanceName: "main", SonarrSeriesID: newSonarrSeriesID(), SeasonNumber: 3}, false},
		{"UserNotFoundError", &sharedErrors.UserNotFoundError{}, false},
		{"InstanceNotFoundError", &sharedErrors.InstanceNotFoundError{Name: "ghost"}, false},
		{"GrabNotFoundError", &sharedErrors.GrabNotFoundError{ID: newGrabID()}, false},
		{"RuntimeConfigNotFoundError", &sharedErrors.RuntimeConfigNotFoundError{}, false},
		{"QbitSettingsNotFoundError", &sharedErrors.QbitSettingsNotFoundError{InstanceID: newInstanceID()}, false},
		{"ScanRunNotFoundError", &sharedErrors.ScanRunNotFoundError{ID: newScanRunID()}, false},
		{"DecisionNotFoundError", &sharedErrors.DecisionNotFoundError{ID: newDecisionID()}, false},
		{"WatchdogBlacklistNotFoundError", &sharedErrors.WatchdogBlacklistNotFoundError{ID: newWBID()}, false},
		{"MediaAssetNotFoundError", &sharedErrors.MediaAssetNotFoundError{Kind: "hash", Key: "deadbeef"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.retriable, sharedErrors.IsRetriable(tc.err))
		})
	}
}

func TestIsRetriable_NestedWrap(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.SonarrUnreachableError{Instance: "main", Cause: errors.New("dial tcp")}
	wrapped := fmt.Errorf("call sonarr: %w", inner)

	assert.True(t, sharedErrors.IsRetriable(wrapped),
		"IsRetriable must walk errors.As chain through fmt.Errorf %%w wrap")
}

func TestIsRetriable_DoubleWrap(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.ScanFailedError{Cause: errors.New("disk full")}
	double := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner))

	assert.True(t, sharedErrors.IsRetriable(double))
}

func TestIsRetriable_Nil(t *testing.T) {
	t.Parallel()
	assert.False(t, sharedErrors.IsRetriable(nil))
}

func TestIsRetriable_PlainError(t *testing.T) {
	t.Parallel()
	assert.False(t, sharedErrors.IsRetriable(errors.New("plain error")))
}

func TestErrorCode_PerType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code string
	}{
		{"SeriesNotFoundError", &sharedErrors.SeriesNotFoundError{ID: newSeriesID()}, "series_not_found"},
		{"SeriesCanonicalLoadError", &sharedErrors.SeriesCanonicalLoadError{ID: newSeriesID()}, "series_canonical_load"},
		{"SonarrUnreachableError", &sharedErrors.SonarrUnreachableError{Instance: "main"}, "sonarr_unreachable"},
		{"SonarrInstanceInvalidError", &sharedErrors.SonarrInstanceInvalidError{Instance: "ghost"}, "sonarr_instance_invalid"},
		{"TMDBRateLimitedError", &sharedErrors.TMDBRateLimitedError{RetryAfter: time.Second}, "tmdb_rate_limited"},
		{"TMDBAuthError", &sharedErrors.TMDBAuthError{}, "tmdb_auth"},
		{"TMDBNotFoundError", &sharedErrors.TMDBNotFoundError{ID: newTMDBID()}, "tmdb_not_found"},
		{"OMDbQuotaExhaustedError", &sharedErrors.OMDbQuotaExhaustedError{ResetAt: time.Now()}, "omdb_quota_exhausted"},
		{"OMDbNotFoundError", &sharedErrors.OMDbNotFoundError{IMDBID: newIMDBID()}, "omdb_not_found"},
		{"OMDbAuthError", &sharedErrors.OMDbAuthError{}, "omdb_auth"},
		{"ScanFailedError", &sharedErrors.ScanFailedError{}, "scan_failed"},
		{"ScanInProgressError", &sharedErrors.ScanInProgressError{}, "scan_in_progress"},
		{"SeriesCacheNotFoundError", &sharedErrors.SeriesCacheNotFoundError{InstanceName: "main", SonarrSeriesID: newSonarrSeriesID()}, "series_cache_not_found"},
		{"EpisodeNotFoundError", &sharedErrors.EpisodeNotFoundError{ID: newEpisodeID()}, "episode_not_found"},
		{"SeasonNotFoundError", &sharedErrors.SeasonNotFoundError{InstanceName: "main", SonarrSeriesID: newSonarrSeriesID(), SeasonNumber: 2}, "season_not_found"},
		{"UserNotFoundError", &sharedErrors.UserNotFoundError{}, "user_not_found"},
		{"InstanceNotFoundError", &sharedErrors.InstanceNotFoundError{Name: "ghost"}, "instance_not_found"},
		{"GrabNotFoundError", &sharedErrors.GrabNotFoundError{ID: newGrabID()}, "grab_not_found"},
		{"RuntimeConfigNotFoundError", &sharedErrors.RuntimeConfigNotFoundError{}, "runtime_config_not_found"},
		{"QbitSettingsNotFoundError", &sharedErrors.QbitSettingsNotFoundError{InstanceID: newInstanceID()}, "qbit_settings_not_found"},
		{"ScanRunNotFoundError", &sharedErrors.ScanRunNotFoundError{ID: newScanRunID()}, "scan_run_not_found"},
		{"DecisionNotFoundError", &sharedErrors.DecisionNotFoundError{ID: newDecisionID()}, "decision_not_found"},
		{"WatchdogBlacklistNotFoundError", &sharedErrors.WatchdogBlacklistNotFoundError{ID: newWBID()}, "watchdog_blacklist_not_found"},
		{"MediaAssetNotFoundError", &sharedErrors.MediaAssetNotFoundError{Kind: "hash", Key: "deadbeef"}, "media_asset_not_found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.code, sharedErrors.ErrorCode(tc.err))
		})
	}
}

func TestErrorCode_NestedWrap(t *testing.T) {
	t.Parallel()

	id := newSeriesID()
	inner := &sharedErrors.SeriesNotFoundError{ID: id}
	wrapped := fmt.Errorf("load: %w", inner)

	assert.Equal(t, "series_not_found", sharedErrors.ErrorCode(wrapped))
}

func TestErrorCode_DoubleWrap(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.TMDBRateLimitedError{RetryAfter: 2 * time.Second}
	double := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner))

	assert.Equal(t, "tmdb_rate_limited", sharedErrors.ErrorCode(double))
}

func TestErrorCode_Nil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", sharedErrors.ErrorCode(nil))
}

func TestErrorCode_Untyped(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "internal_error", sharedErrors.ErrorCode(errors.New("plain")))
}

func TestErrorMessages_IncludeIdentifiers(t *testing.T) {
	t.Parallel()

	id := newSeriesID()
	seriesNF := &sharedErrors.SeriesNotFoundError{ID: id}
	assert.Contains(t, seriesNF.Error(), fmt.Sprintf("%d", id))

	tmdbID := newTMDBID()
	tmdbNF := &sharedErrors.TMDBNotFoundError{ID: tmdbID}
	assert.Contains(t, tmdbNF.Error(), fmt.Sprintf("%d", tmdbID))

	imdbID := newIMDBID()
	omdbNF := &sharedErrors.OMDbNotFoundError{IMDBID: imdbID}
	assert.Contains(t, omdbNF.Error(), imdbID)

	resetAt := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	omdbQ := &sharedErrors.OMDbQuotaExhaustedError{ResetAt: resetAt}
	assert.Contains(t, omdbQ.Error(), "2026-06-18T10:00:00Z")
}

func TestTMDBRateLimited_MessageRoundsToSeconds(t *testing.T) {
	t.Parallel()

	e := &sharedErrors.TMDBRateLimitedError{RetryAfter: 1500 * time.Millisecond}
	assert.Equal(t, "tmdb rate limited; retry after 2s", e.Error())
}

func TestSeriesCanonicalLoad_UnwrapPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("db timeout")
	e := &sharedErrors.SeriesCanonicalLoadError{ID: newSeriesID(), Cause: cause}

	assert.ErrorIs(t, e, cause, "Unwrap must expose Cause for errors.Is")
}

func TestSonarrUnreachable_UnwrapPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection refused")
	e := &sharedErrors.SonarrUnreachableError{Instance: "main", Cause: cause}

	assert.ErrorIs(t, e, cause)
}

func TestScanFailed_UnwrapPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("disk full")
	e := &sharedErrors.ScanFailedError{Cause: cause}

	assert.ErrorIs(t, e, cause)
}

func TestIsRetriable_NewTypes_NestedWrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"UserNotFoundError", &sharedErrors.UserNotFoundError{}},
		{"GrabNotFoundError", &sharedErrors.GrabNotFoundError{ID: newGrabID()}},
		{"ScanRunNotFoundError", &sharedErrors.ScanRunNotFoundError{ID: newScanRunID()}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := fmt.Errorf("ctx: %w", tc.err)
			assert.False(t, sharedErrors.IsRetriable(wrapped))
		})
	}
}

func TestErrorCode_NewTypes_NestedWrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code string
	}{
		{"UserNotFoundError", &sharedErrors.UserNotFoundError{}, "user_not_found"},
		{"GrabNotFoundError", &sharedErrors.GrabNotFoundError{ID: newGrabID()}, "grab_not_found"},
		{"ScanRunNotFoundError", &sharedErrors.ScanRunNotFoundError{ID: newScanRunID()}, "scan_run_not_found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := fmt.Errorf("ctx: %w", tc.err)
			assert.Equal(t, tc.code, sharedErrors.ErrorCode(wrapped))
		})
	}
}

func TestNilCauseDoesNotPanic(t *testing.T) {
	t.Parallel()

	cases := []error{
		&sharedErrors.SeriesCanonicalLoadError{ID: newSeriesID(), Cause: nil},
		&sharedErrors.SonarrUnreachableError{Instance: "main", Cause: nil},
		&sharedErrors.ScanFailedError{Cause: nil},
	}
	for _, e := range cases {
		// Error() must not panic on nil cause and must produce non-empty text.
		assert.NotEmpty(t, e.Error())
	}
}
