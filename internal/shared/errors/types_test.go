package errors_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// newSeriesID returns a non-shared series id for use as a test sentinel.
// Per D-0 rule, tests must not share fixed ids across cases.
func newSeriesID() int64 { return rand.Int64N(1_000_000) + 1 }

func newTMDBID() int { return int(rand.Int32N(1_000_000) + 1) }

func newIMDBID() string { return fmt.Sprintf("tt%07d", rand.Int32N(9_999_999)+1) }

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
