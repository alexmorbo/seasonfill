package radarr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestRadarrMapWebhookEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		wantTyp webhook.MovieEventType
		wantID  int
		wantHF  bool
		wantErr bool
	}{
		{"movie_added", `{"eventType":"MovieAdded","movie":{"id":7,"title":"Dune","tmdbId":438631,"titleSlug":"dune-2021"}}`, webhook.MovieEventTypeUpsert, 7, false, false},
		{"grab", `{"eventType":"Grab","movie":{"id":7}}`, webhook.MovieEventTypeGrabbed, 7, false, false},
		{"download_import", `{"eventType":"Download","movie":{"id":7},"movieFile":{"size":5000000000}}`, webhook.MovieEventTypeUpsert, 7, true, false},
		{"file_delete", `{"eventType":"MovieFileDelete","movie":{"id":7},"deletedFiles":true}`, webhook.MovieEventTypeUpsert, 7, false, false},
		{"movie_delete", `{"eventType":"MovieDelete","movie":{"id":7}}`, webhook.MovieEventTypeDeleted, 7, false, false},
		{"test", `{"eventType":"Test"}`, webhook.MovieEventTypeUnsupported, 0, false, false},
		{"health", `{"eventType":"Health"}`, webhook.MovieEventTypeUnsupported, 0, false, false},
		{"unknown", `{"eventType":"Bogus"}`, webhook.MovieEventTypeUnsupported, 0, false, false},
		{"malformed", `{bad`, "", 0, false, true},
		{"missing_type", `{"movie":{"id":1}}`, "", 0, false, true},
		{"empty", ``, "", 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev, err := MapWebhookEvent([]byte(tc.body), "radarr-main")
			if tc.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ErrMalformedPayload)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantTyp, ev.Type)
			assert.Equal(t, tc.wantID, ev.RadarrMovieID)
			assert.Equal(t, tc.wantHF, ev.HasFile)
			assert.Equal(t, domain.InstanceName("radarr-main"), ev.InstanceName)
		})
	}
}

// TestRadarrMapWebhookEvent_MonitoredDefaultAndOverride — Monitored defaults to
// true when the payload omits movie.monitored, and is overridden when present.
func TestRadarrMapWebhookEvent_MonitoredDefaultAndOverride(t *testing.T) {
	t.Parallel()

	def, err := MapWebhookEvent([]byte(`{"eventType":"MovieAdded","movie":{"id":7}}`), "r1")
	require.NoError(t, err)
	assert.True(t, def.Monitored, "default true when omitted")

	off, err := MapWebhookEvent([]byte(`{"eventType":"MovieAdded","movie":{"id":7,"monitored":false}}`), "r1")
	require.NoError(t, err)
	assert.False(t, off.Monitored, "override from payload")
}

// TestRadarrMapWebhookEvent_FieldMapping — the movie block maps through to the
// domain event fields the F-21 helper consumes.
func TestRadarrMapWebhookEvent_FieldMapping(t *testing.T) {
	t.Parallel()
	ev, err := MapWebhookEvent([]byte(`{"eventType":"MovieAdded","instanceName":"ignored","movie":{"id":7,"title":"Dune","titleSlug":"dune-2021","year":2021,"tmdbId":438631,"imdbId":"tt1160419","minimumAvailability":"released"}}`), "radarr-main")
	require.NoError(t, err)
	assert.Equal(t, "Dune", ev.Title)
	assert.Equal(t, "dune-2021", ev.TitleSlug)
	assert.Equal(t, 2021, ev.Year)
	assert.Equal(t, 438631, ev.TMDBID)
	assert.Equal(t, "tt1160419", ev.IMDBID)
	assert.Equal(t, "released", ev.MinimumAvailability)
	assert.Equal(t, "MovieAdded", ev.RawEventType)
	// instanceName from the URL path, NOT the payload.
	assert.Equal(t, domain.InstanceName("radarr-main"), ev.InstanceName)
}

// TestRadarrMapWebhookEvent_GrabCarriesDownloadID — ADR-0023 B1.2: the Grab
// event is classified distinctly AND the already-parsed downloadId reaches the
// domain event (it used to be dropped on the floor). Non-grab events leave
// DownloadID empty.
func TestRadarrMapWebhookEvent_GrabCarriesDownloadID(t *testing.T) {
	t.Parallel()

	const hash = "0123456789ABCDEF0123456789abcdef01234567"
	ev, err := MapWebhookEvent([]byte(
		`{"eventType":"Grab","downloadId":"`+hash+`","movie":{"id":7,"title":"Dune","tmdbId":438631}}`,
	), "radarr-main")
	require.NoError(t, err)
	assert.Equal(t, webhook.MovieEventTypeGrabbed, ev.Type)
	assert.Equal(t, hash, ev.DownloadID, "downloadId passes through verbatim; normalisation is the domain's job")
	assert.Equal(t, 7, ev.RadarrMovieID)
	assert.Equal(t, "Grab", ev.RawEventType)
	assert.False(t, ev.HasFile, "a grab is not an import")

	// Whitespace-only downloadId normalises to "" (→ silent no-op downstream).
	blank, err := MapWebhookEvent([]byte(`{"eventType":"Grab","downloadId":"   ","movie":{"id":7}}`), "radarr-main")
	require.NoError(t, err)
	assert.Empty(t, blank.DownloadID)

	// Non-grab events carry no downloadId.
	added, err := MapWebhookEvent([]byte(`{"eventType":"MovieAdded","movie":{"id":7}}`), "radarr-main")
	require.NoError(t, err)
	assert.Empty(t, added.DownloadID)
	assert.Equal(t, webhook.MovieEventTypeUpsert, added.Type)
}
