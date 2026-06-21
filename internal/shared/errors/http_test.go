package errors_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func TestStatusCode_AllTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"SeriesNotFoundError", &sharedErrors.SeriesNotFoundError{ID: newSeriesID()}, http.StatusNotFound},
		{"TMDBNotFoundError", &sharedErrors.TMDBNotFoundError{ID: newTMDBID()}, http.StatusNotFound},
		{"OMDbNotFoundError", &sharedErrors.OMDbNotFoundError{IMDBID: newIMDBID()}, http.StatusNotFound},
		{"ScanInProgressError", &sharedErrors.ScanInProgressError{}, http.StatusConflict},
		{"SonarrInstanceInvalidError", &sharedErrors.SonarrInstanceInvalidError{Instance: "ghost"}, http.StatusBadRequest},
		{"TMDBRateLimitedError", &sharedErrors.TMDBRateLimitedError{RetryAfter: time.Second}, http.StatusTooManyRequests},
		{"TMDBAuthError", &sharedErrors.TMDBAuthError{}, http.StatusBadGateway},
		{"OMDbAuthError", &sharedErrors.OMDbAuthError{}, http.StatusBadGateway},
		{"SonarrUnreachableError", &sharedErrors.SonarrUnreachableError{Instance: "main"}, http.StatusBadGateway},
		{"OMDbQuotaExhaustedError", &sharedErrors.OMDbQuotaExhaustedError{ResetAt: time.Now().Add(time.Hour)}, http.StatusServiceUnavailable},
		{"ScanFailedError", &sharedErrors.ScanFailedError{Cause: errors.New("boom")}, http.StatusInternalServerError},
		{"SeriesCanonicalLoadError", &sharedErrors.SeriesCanonicalLoadError{ID: newSeriesID(), Cause: errors.New("boom")}, http.StatusInternalServerError},
		{"SeriesCacheNotFoundError", &sharedErrors.SeriesCacheNotFoundError{InstanceName: "main", SonarrSeriesID: newSonarrSeriesID()}, http.StatusNotFound},
		{"EpisodeNotFoundError", &sharedErrors.EpisodeNotFoundError{ID: newEpisodeID()}, http.StatusNotFound},
		{"SeasonNotFoundError", &sharedErrors.SeasonNotFoundError{InstanceName: "main", SonarrSeriesID: newSonarrSeriesID(), SeasonNumber: 1}, http.StatusNotFound},
		{"UserNotFoundError", &sharedErrors.UserNotFoundError{}, http.StatusNotFound},
		{"InstanceNotFoundError", &sharedErrors.InstanceNotFoundError{Name: "ghost"}, http.StatusNotFound},
		{"GrabNotFoundError", &sharedErrors.GrabNotFoundError{ID: newGrabID()}, http.StatusNotFound},
		{"RuntimeConfigNotFoundError", &sharedErrors.RuntimeConfigNotFoundError{}, http.StatusNotFound},
		{"QbitSettingsNotFoundError", &sharedErrors.QbitSettingsNotFoundError{InstanceID: newInstanceID()}, http.StatusNotFound},
		{"ScanRunNotFoundError", &sharedErrors.ScanRunNotFoundError{ID: newScanRunID()}, http.StatusNotFound},
		{"DecisionNotFoundError", &sharedErrors.DecisionNotFoundError{ID: newDecisionID()}, http.StatusNotFound},
		{"WatchdogBlacklistNotFoundError", wbErr(), http.StatusNotFound},
		{"MediaAssetNotFoundError", &sharedErrors.MediaAssetNotFoundError{Kind: "hash", Key: "deadbeef"}, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.status, sharedErrors.StatusCode(tc.err))
		})
	}
}

func TestStatusCode_NestedWrap(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.SeriesNotFoundError{ID: newSeriesID()}
	wrapped := fmt.Errorf("ctx: %w", inner)

	assert.Equal(t, http.StatusNotFound, sharedErrors.StatusCode(wrapped))
}

func TestStatusCode_DoubleWrap(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.TMDBRateLimitedError{RetryAfter: 3 * time.Second}
	double := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner))

	assert.Equal(t, http.StatusTooManyRequests, sharedErrors.StatusCode(double))
}

func TestStatusCode_Nil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, sharedErrors.StatusCode(nil))
}

func TestStatusCode_Untyped(t *testing.T) {
	t.Parallel()
	assert.Equal(t, http.StatusInternalServerError, sharedErrors.StatusCode(errors.New("boom")))
}

func TestStatusCode_WatchdogBlacklistNF_NestedWrap(t *testing.T) {
	t.Parallel()

	inner := wbErr()
	wrapped := fmt.Errorf("delete: %w", inner)

	assert.Equal(t, http.StatusNotFound, sharedErrors.StatusCode(wrapped))
}

func TestStatusCode_TripleNestedRetainsDeepestTypedMatch(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.OMDbQuotaExhaustedError{ResetAt: time.Now().Add(2 * time.Hour)}
	wrapped := fmt.Errorf("a: %w",
		fmt.Errorf("b: %w",
			fmt.Errorf("c: %w", inner)))

	assert.Equal(t, http.StatusServiceUnavailable, sharedErrors.StatusCode(wrapped))
}
