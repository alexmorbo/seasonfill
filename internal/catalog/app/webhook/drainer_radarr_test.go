package webhook

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestDrainer_RadarrRow_RoutesToRadarrProcess — a row on a radarr-typed instance
// is drained via the radarr map+process; the sonarr Process is NOT invoked.
func TestDrainer_RadarrRow_RoutesToRadarrProcess(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:     oneShotClaim([]ports.WebhookInboxRow{{ID: 1, InstanceName: "radarr-main", EventType: "MovieAdded", Payload: []byte(`{"eventType":"MovieAdded","movie":{"id":7}}`)}}),
		MarkSuccessFunc:  func(context.Context, int64) error { return nil },
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
	}

	sonarrCalled := false
	radarrCalled := false
	d := NewDrainer(DrainerDeps{
		Inbox:         mock,
		Process:       func(context.Context, webhook.Event) error { sonarrCalled = true; return nil },
		MapEvent:      mapValid,
		Clock:         fake,
		AttemptCap:    3,
		PerJobTimeout: 5 * time.Second,
		InstanceTypeResolver: func(name string) string {
			if name == "radarr-main" {
				return scan.InstanceTypeRadarr
			}
			return scan.InstanceTypeSonarr
		},
		RadarrMapEvent: func(payload []byte, inst domain.InstanceName) (webhook.MovieEvent, error) {
			return webhook.MovieEvent{Type: webhook.MovieEventTypeUpsert, InstanceName: inst, RadarrMovieID: 7, RawEventType: "MovieAdded"}, nil
		},
		RadarrProcess: func(context.Context, webhook.MovieEvent) error { radarrCalled = true; return nil },
	})

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkSuccessCalls(), 1)
	assert.Equal(t, int64(1), mock.MarkSuccessCalls()[0].ID)
	assert.True(t, radarrCalled, "radarr process invoked")
	assert.False(t, sonarrCalled, "sonarr process must NOT run for a radarr row")
}

// TestDrainer_NilResolver_SonarrPathUnchanged — a row on any instance with a nil
// InstanceTypeResolver drains via the sonarr path byte-identically (the radarr
// hooks are never consulted).
func TestDrainer_NilResolver_SonarrPathUnchanged(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:    oneShotClaim([]ports.WebhookInboxRow{{ID: 2, InstanceName: "main", EventType: "Download", Payload: []byte("{}")}}),
		MarkSuccessFunc: func(context.Context, int64) error { return nil },
	}

	sonarrCalled := false
	// Nil InstanceTypeResolver + nil radarr hooks — the guarded branch is skipped.
	d := newDrainer(t, mock, func(context.Context, webhook.Event) error { sonarrCalled = true; return nil }, fake)

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkSuccessCalls(), 1)
	assert.True(t, sonarrCalled, "sonarr process runs when no resolver is wired")
}

// TestDrainer_RadarrRow_NilRadarrProcess_FallsThroughToSonarr — a radarr-typed
// row still falls through to the sonarr path when RadarrProcess is nil (defensive
// guard: the branch requires BOTH a resolver AND a radarr process).
func TestDrainer_RadarrRow_NilRadarrProcess_FallsThroughToSonarr(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:     oneShotClaim([]ports.WebhookInboxRow{{ID: 3, InstanceName: "radarr-main", EventType: "Download", Payload: []byte("{}")}}),
		MarkSuccessFunc:  func(context.Context, int64) error { return nil },
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
	}

	sonarrCalled := false
	d := NewDrainer(DrainerDeps{
		Inbox:                mock,
		Process:              func(context.Context, webhook.Event) error { sonarrCalled = true; return nil },
		MapEvent:             mapValid,
		Clock:                fake,
		AttemptCap:           3,
		PerJobTimeout:        5 * time.Second,
		InstanceTypeResolver: func(string) string { return scan.InstanceTypeRadarr },
		// RadarrProcess nil → guarded branch skipped → sonarr path.
	})

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkSuccessCalls(), 1)
	assert.True(t, sonarrCalled, "nil RadarrProcess falls through to sonarr")
}

// TestDrainer_RadarrDedupKey_IncludesDownloadID — two Grab rows for the SAME
// movie with DIFFERENT torrent hashes (an original grab and an upgrade grab)
// in ONE drain pass must BOTH be processed. Before the B1.3 fold-in the key
// omitted DownloadID and the second hash was silently dropped, so its
// torrent_movie_map row never existed.
func TestDrainer_RadarrDedupKey_IncludesDownloadID(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	rows := []ports.WebhookInboxRow{
		{ID: 1, InstanceName: "radarr-main", EventType: "Grab", Payload: []byte(`{"downloadId":"aaaa"}`)},
		{ID: 2, InstanceName: "radarr-main", EventType: "Grab", Payload: []byte(`{"downloadId":"bbbb"}`)},
	}
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc:     oneShotClaim(rows),
		MarkSuccessFunc:  func(context.Context, int64) error { return nil },
		ReclaimStaleFunc: func(context.Context, time.Time) (int64, error) { return 0, nil },
	}

	var (
		mu       sync.Mutex
		gotHash  []string
		payloadN int
	)
	d := NewDrainer(DrainerDeps{
		Inbox:                mock,
		Process:              func(context.Context, webhook.Event) error { return nil },
		MapEvent:             mapValid,
		Clock:                fake,
		AttemptCap:           3,
		PerJobTimeout:        5 * time.Second,
		InstanceTypeResolver: func(string) string { return scan.InstanceTypeRadarr },
		RadarrMapEvent: func(_ []byte, inst domain.InstanceName) (webhook.MovieEvent, error) {
			mu.Lock()
			payloadN++
			hash := "aaaa"
			if payloadN == 2 {
				hash = "bbbb"
			}
			mu.Unlock()
			// Same movie id, same raw event type, DIFFERENT download id.
			return webhook.MovieEvent{
				Type: webhook.MovieEventTypeUpsert, InstanceName: inst,
				RadarrMovieID: 7, RawEventType: "Grab", DownloadID: hash,
			}, nil
		},
		RadarrProcess: func(_ context.Context, e webhook.MovieEvent) error {
			mu.Lock()
			gotHash = append(gotHash, e.DownloadID)
			mu.Unlock()
			return nil
		},
	})

	d.drainOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	assert.ElementsMatch(t, []string{"aaaa", "bbbb"}, gotHash,
		"both grabs must be processed — DownloadID is part of the dedup key")
	assert.Len(t, mock.MarkSuccessCalls(), 2)
}
