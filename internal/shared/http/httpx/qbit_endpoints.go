package httpx

import (
	"net/http"
	"strings"
)

// qbitV2Prefix is the qBittorrent Web API namespace. Every supported
// endpoint lives under it. Stripping the prefix once lets the switch
// below match short, readable action paths.
const qbitV2Prefix = "/api/v2/"

// qbitEndpointLabels maps the post-prefix tail of a qBit V2 request to
// a stable, low-cardinality label. The keyset is closed — new V2
// actions added after this story land in the `other` bucket until they
// are added here. The label values use snake_case (matching the rest
// of the seasonfill metric label vocabulary) rather than the literal
// path so a Grafana legend reads "torrents_set_category" not
// "/torrents/setCategory".
//
// Endpoints below cover (a) the five actions actually used by
// internal/shared/clients/qbit today (auth_login, app_version,
// sync_maindata, torrents_info, torrents_trackers) and (b) the
// additional actions a future B-32 expansion is most likely to wire
// (add/delete/pause/resume/recheck/setCategory/addTags/removeTags/
// setLocation/preferences/transfer). Pre-listing them keeps the
// metric label set stable across B-32 — operator dashboards built
// today don't need to be retouched once the new wrappers ship.
var qbitEndpointLabels = map[string]string{
	"auth/login":                  "auth_login",
	"auth/logout":                 "auth_logout",
	"app/version":                 "app_version",
	"app/webapiVersion":           "app_webapi_version",
	"app/buildInfo":               "app_build_info",
	"app/preferences":             "app_preferences",
	"app/setPreferences":          "app_set_preferences",
	"sync/maindata":               "sync_maindata",
	"torrents/info":               "torrents_info",
	"torrents/properties":         "torrents_properties",
	"torrents/files":              "torrents_files",
	"torrents/trackers":           "torrents_trackers",
	"torrents/add":                "torrents_add",
	"torrents/delete":             "torrents_delete",
	"torrents/pause":              "torrents_pause",
	"torrents/resume":             "torrents_resume",
	"torrents/recheck":            "torrents_recheck",
	"torrents/setCategory":        "torrents_set_category",
	"torrents/categories":         "torrents_categories",
	"torrents/createCategory":     "torrents_create_category",
	"torrents/addTags":            "torrents_add_tags",
	"torrents/removeTags":         "torrents_remove_tags",
	"torrents/setLocation":        "torrents_set_location",
	"transfer/info":               "transfer_info",
	"transfer/setSpeedLimitsMode": "transfer_set_speed_limits_mode",
}

// QbitEndpointFor returns a stable label for an outbound qBittorrent
// Web API request. Behaviour:
//
//   - empty / unrecognised path        -> "unknown"
//   - "/api/v2" or "/api/v2/" exactly  -> "root"
//   - mapped action under /api/v2/...  -> the table label
//   - unmapped tail under /api/v2/...  -> "other"
//
// The mapper consumes only req.URL.Path — query string, form body, and
// hash parameters never enter the label, keeping cardinality bounded
// regardless of torrent corpus size. Implementations of qBit Web API
// HTTP method semantics vary by version (some actions accept POST,
// some both), so the mapper is HTTP-method agnostic.
func QbitEndpointFor(r *http.Request) string {
	if r == nil || r.URL == nil {
		return "unknown"
	}
	path := r.URL.Path
	if path == "" {
		return "unknown"
	}
	// Bare /api/v2 or /api/v2/ -> root. Library never hits these in
	// practice but the closed-set bucket prevents a stray probe from
	// silently joining "unknown".
	if path == "/api/v2" || path == "/api/v2/" {
		return "root"
	}
	if !strings.HasPrefix(path, qbitV2Prefix) {
		return "unknown"
	}
	tail := strings.TrimPrefix(path, qbitV2Prefix)
	// Strip a trailing slash so "torrents/info/" still hits the table.
	tail = strings.TrimSuffix(tail, "/")
	if tail == "" {
		return "root"
	}
	if label, ok := qbitEndpointLabels[tail]; ok {
		return label
	}
	return "other"
}
