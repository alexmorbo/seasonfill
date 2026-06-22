package httpx

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQbitEndpointFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		// Auth.
		{"/api/v2/auth/login", "auth_login"},
		{"/api/v2/auth/logout", "auth_logout"},

		// App.
		{"/api/v2/app/version", "app_version"},
		{"/api/v2/app/webapiVersion", "app_webapi_version"},
		{"/api/v2/app/buildInfo", "app_build_info"},
		{"/api/v2/app/preferences", "app_preferences"},
		{"/api/v2/app/setPreferences", "app_set_preferences"},

		// Sync.
		{"/api/v2/sync/maindata", "sync_maindata"},

		// Torrents — primary wire targets.
		{"/api/v2/torrents/info", "torrents_info"},
		{"/api/v2/torrents/properties", "torrents_properties"},
		{"/api/v2/torrents/files", "torrents_files"},
		{"/api/v2/torrents/trackers", "torrents_trackers"},

		// Torrents — B-32 forward-compatible labels.
		{"/api/v2/torrents/add", "torrents_add"},
		{"/api/v2/torrents/delete", "torrents_delete"},
		{"/api/v2/torrents/pause", "torrents_pause"},
		{"/api/v2/torrents/resume", "torrents_resume"},
		{"/api/v2/torrents/recheck", "torrents_recheck"},
		{"/api/v2/torrents/setCategory", "torrents_set_category"},
		{"/api/v2/torrents/categories", "torrents_categories"},
		{"/api/v2/torrents/createCategory", "torrents_create_category"},
		{"/api/v2/torrents/addTags", "torrents_add_tags"},
		{"/api/v2/torrents/removeTags", "torrents_remove_tags"},
		{"/api/v2/torrents/setLocation", "torrents_set_location"},

		// Transfer.
		{"/api/v2/transfer/info", "transfer_info"},
		{"/api/v2/transfer/setSpeedLimitsMode", "transfer_set_speed_limits_mode"},

		// Trailing slash variants — still hit the table.
		{"/api/v2/torrents/info/", "torrents_info"},
		{"/api/v2/sync/maindata/", "sync_maindata"},

		// Bare prefix paths.
		{"/api/v2", "root"},
		{"/api/v2/", "root"},

		// Unmapped V2 actions -> other.
		{"/api/v2/torrents/exportTorrent", "other"},
		{"/api/v2/log/main", "other"},
		{"/api/v2/rss/items", "other"},

		// Non-V2 paths -> unknown.
		{"/api/v1/torrents/info", "unknown"},
		{"/login", "unknown"},
		{"/", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://qbit.local"+tc.raw, nil)
			got := QbitEndpointFor(req)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestQbitEndpointFor_NilRequest_BucketsAsUnknown guards the defensive
// nil-receiver path. A misconfigured caller passing nil through must
// not panic the metrics transport.
func TestQbitEndpointFor_NilRequest_BucketsAsUnknown(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "unknown", QbitEndpointFor(nil))
}

// TestQbitEndpointFor_NilURL_BucketsAsUnknown — *http.Request with a
// nil URL field is technically valid (e.g. a zero-value Request used
// as a probe). The mapper must absorb it without panic.
func TestQbitEndpointFor_NilURL_BucketsAsUnknown(t *testing.T) {
	t.Parallel()
	req := &http.Request{}
	assert.Equal(t, "unknown", QbitEndpointFor(req))
}
