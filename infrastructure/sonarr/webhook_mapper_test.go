package sonarr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/webhook"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("fixtures", name))
	require.NoError(t, err, "fixture %s must be readable", name)
	return b
}

func TestMapWebhookEvent_KnownEventTypes(t *testing.T) {
	t.Parallel()
	type want struct {
		typ          webhook.EventType
		downloadID   string
		releaseTitle string
		indexer      string
		seriesID     int
		seasonNumber int
		nonEmptyMsg  bool
		rawEventType string
	}
	cases := []struct {
		name    string
		fixture string
		want    want
	}{
		{
			name:    "grab event maps to grabbed",
			fixture: "webhook-grab.json",
			want: want{
				typ:          webhook.EventTypeGrabbed,
				downloadID:   "ABCDEF1234567890",
				releaseTitle: "Hijack.S02.1080p.WEB-DL.DDP5.1.H.264-TEST",
				indexer:      "RuTracker (Prowlarr)",
				seriesID:     122,
				seasonNumber: 2,
				rawEventType: "Grab",
			},
		},
		{
			name:    "download event maps to imported",
			fixture: "webhook-import.json",
			want: want{
				typ:          webhook.EventTypeImported,
				downloadID:   "ABCDEF1234567890",
				releaseTitle: "",
				indexer:      "",
				seriesID:     122,
				seasonNumber: 2,
				rawEventType: "Download",
			},
		},
		{
			name:    "manual interaction required maps to import_failed",
			fixture: "webhook-import-failed.json",
			want: want{
				typ:          webhook.EventTypeImportFailed,
				downloadID:   "ABCDEF1234567890",
				releaseTitle: "Hijack.S02.1080p.WEB-DL.DDP5.1.H.264-TEST",
				indexer:      "RuTracker (Prowlarr)",
				seriesID:     122,
				seasonNumber: 2,
				nonEmptyMsg:  true,
				rawEventType: "ManualInteractionRequired",
			},
		},
		{
			name:    "test event maps to unsupported",
			fixture: "webhook-test.json",
			want: want{
				typ:          webhook.EventTypeUnsupported,
				rawEventType: "Test",
			},
		},
		{
			name:    "rename event maps to unsupported",
			fixture: "webhook-unsupported.json",
			want: want{
				typ:          webhook.EventTypeUnsupported,
				seriesID:     122,
				seasonNumber: 2,
				rawEventType: "Rename",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := loadFixture(t, tc.fixture)
			ev, err := MapWebhookEvent(body, "sonarr-main")
			require.NoError(t, err)
			assert.Equal(t, tc.want.typ, ev.Type, "Type")
			assert.Equal(t, tc.want.downloadID, ev.DownloadID, "DownloadID")
			assert.Equal(t, tc.want.releaseTitle, ev.ReleaseTitle, "ReleaseTitle")
			assert.Equal(t, tc.want.indexer, ev.Indexer, "Indexer")
			assert.Equal(t, tc.want.seriesID, ev.SeriesID, "SeriesID")
			assert.Equal(t, tc.want.seasonNumber, ev.SeasonNumber, "SeasonNumber")
			assert.Equal(t, tc.want.rawEventType, ev.RawEventType, "RawEventType")
			assert.Equal(t, "sonarr-main", ev.InstanceName, "InstanceName")
			assert.False(t, ev.OccurredAt.IsZero(), "OccurredAt must be populated")
			if tc.want.nonEmptyMsg {
				assert.NotEmpty(t, ev.Message, "Message should carry failure detail")
			} else {
				assert.Empty(t, ev.Message, "Message should be empty on non-failure")
			}
		})
	}
}

func TestMapWebhookEvent_UnknownEventTypeIsUnsupported(t *testing.T) {
	t.Parallel()
	body := []byte(`{"eventType":"SomeFutureSonarrEvent","instanceName":"Sonarr"}`)
	ev, err := MapWebhookEvent(body, "sonarr-main")
	require.NoError(t, err)
	assert.Equal(t, webhook.EventTypeUnsupported, ev.Type)
	assert.Equal(t, "SomeFutureSonarrEvent", ev.RawEventType)
	assert.Equal(t, "sonarr-main", ev.InstanceName)
}

func TestMapWebhookEvent_AliasMapCaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := map[string]webhook.EventType{
		"grab":                      webhook.EventTypeGrabbed,
		"GRAB":                      webhook.EventTypeGrabbed,
		"  Grab  ":                  webhook.EventTypeGrabbed,
		"download":                  webhook.EventTypeImported,
		"Import":                    webhook.EventTypeImported,
		"ManualInteractionRequired": webhook.EventTypeImportFailed,
		"DownloadFailure":           webhook.EventTypeImportFailed,
		"ImportFailure":             webhook.EventTypeImportFailed,
		"Test":                      webhook.EventTypeUnsupported,
		"Rename":                    webhook.EventTypeUnsupported,
		"Health":                    webhook.EventTypeUnsupported,
		"HealthRestored":            webhook.EventTypeUnsupported,
		"ApplicationUpdate":         webhook.EventTypeUnsupported,
		"SeriesAdd":                 webhook.EventTypeUnsupported,
		"SeriesDelete":              webhook.EventTypeUnsupported,
		"EpisodeFileDelete":         webhook.EventTypeUnsupported,
	}
	for raw, want := range cases {
		raw, want := raw, want
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, classifyEventType(raw))
		})
	}
}

func TestMapWebhookEvent_MalformedJSON(t *testing.T) {
	t.Parallel()
	body := []byte(`{"eventType":"Grab",`) // truncated
	_, err := MapWebhookEvent(body, "sonarr-main")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMalformedPayload), "must wrap ErrMalformedPayload")
}

func TestMapWebhookEvent_EmptyBody(t *testing.T) {
	t.Parallel()
	_, err := MapWebhookEvent(nil, "sonarr-main")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMalformedPayload))
}

func TestMapWebhookEvent_MissingEventType(t *testing.T) {
	t.Parallel()
	body := []byte(`{"instanceName":"Sonarr","downloadId":"X"}`)
	_, err := MapWebhookEvent(body, "sonarr-main")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMalformedPayload))
}

func TestMapWebhookEvent_EventTimestampParsed(t *testing.T) {
	t.Parallel()
	body := loadFixture(t, "webhook-grab.json")
	ev, err := MapWebhookEvent(body, "sonarr-main")
	require.NoError(t, err)
	// Fixture pins eventTimestamp to 2026-05-20T18:30:00Z — mapper must
	// honour it, not call time.Now().
	want := time.Date(2026, 5, 20, 18, 30, 0, 0, time.UTC)
	assert.True(t, ev.OccurredAt.Equal(want), "want %s got %s", want, ev.OccurredAt)
}

func TestMapWebhookEvent_OccurredAtFallback(t *testing.T) {
	t.Parallel()
	// Payload omits eventTimestamp → mapper falls back to time.Now.
	body := []byte(`{"eventType":"Test","instanceName":"Sonarr"}`)
	before := time.Now().UTC().Add(-time.Second)
	ev, err := MapWebhookEvent(body, "sonarr-main")
	require.NoError(t, err)
	after := time.Now().UTC().Add(time.Second)
	assert.True(t, !ev.OccurredAt.Before(before) && !ev.OccurredAt.After(after),
		"OccurredAt fallback %s must be within [%s, %s]", ev.OccurredAt, before, after)
}

func TestJoinStatusMessages(t *testing.T) {
	t.Parallel()
	t.Run("empty input returns empty string", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", joinStatusMessages(nil))
		assert.Equal(t, "", joinStatusMessages([]webhookStatusMessageDTO{}))
	})
	t.Run("blank entries skipped", func(t *testing.T) {
		t.Parallel()
		got := joinStatusMessages([]webhookStatusMessageDTO{
			{Title: "  ", Messages: []string{"", "   "}},
		})
		assert.Equal(t, "", got)
	})
	t.Run("title and message joined", func(t *testing.T) {
		t.Parallel()
		got := joinStatusMessages([]webhookStatusMessageDTO{
			{Title: "Import failed", Messages: []string{"file rejected by quality profile"}},
			{Title: "Sample", Messages: []string{"too small"}},
		})
		assert.Equal(t, "Import failed: file rejected by quality profile\nSample: too small", got)
	})
}
