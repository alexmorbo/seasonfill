package sonarr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestMapWebhookEvent_EpisodeFileDelete(t *testing.T) {
	t.Parallel()
	ev, err := MapWebhookEvent([]byte(`{"eventType":"EpisodeFileDelete","series":{"id":140,"title":"Rick and Morty"}}`), "homelab")
	require.NoError(t, err)
	assert.Equal(t, webhook.EventTypeEpisodeFileDelete, ev.Type)
	assert.Equal(t, domain.SonarrSeriesID(140), ev.SeriesID)
}

func TestMapWebhookEvent_GrabReleaseSizePopulated(t *testing.T) {
	t.Parallel()
	payload := []byte(`{
		"eventType": "Grab",
		"downloadId": "0123456789abcdef0123456789abcdef01234567",
		"release": {"releaseTitle": "Severance.S02.WEBDL-2160p", "indexer": "trackerx", "size": 13325829734},
		"series": {"id": 122, "title": "Severance"},
		"episodes": [{"seasonNumber": 2, "episodeNumber": 1}]
	}`)
	ev, err := MapWebhookEvent(payload, "main")
	require.NoError(t, err)
	require.Equal(t, int64(13325829734), ev.ReleaseSize)
}

func TestMapWebhookEvent_GrabReleaseSizeAbsent_ZeroValue(t *testing.T) {
	t.Parallel()
	payload := []byte(`{
		"eventType": "Grab",
		"downloadId": "0123456789abcdef0123456789abcdef01234567",
		"release": {"releaseTitle": "Severance.S02.WEBDL-2160p", "indexer": "trackerx"},
		"series": {"id": 122, "title": "Severance"},
		"episodes": [{"seasonNumber": 2, "episodeNumber": 1}]
	}`)
	ev, err := MapWebhookEvent(payload, "main")
	require.NoError(t, err)
	require.Equal(t, int64(0), ev.ReleaseSize)
}
