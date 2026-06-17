package qbit

import "testing"

func TestIsTrackerDown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"empty", "", false},
		{"exact down", "Tracker is down", true},
		{"contains timeout", "request timeout while contacting tracker", true},
		{"contains refused", "connection refused", true},
		{"libtorrent 503", "Service Unavailable", true},
		{"libtorrent 500", "Internal Server Error", true},
		{"libtorrent 401", "Unauthorized", true},
		{"libtorrent 403", "Forbidden", true},
		{"unknown http", "(unknown http error)", true},
		{"not down msg", "Torrent not registered with this tracker", false},
		{"empty-looking", "Working", false},
		{"unregistered substring not matched", "unregistered", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsTrackerDown(tc.msg)
			if got != tc.want {
				t.Fatalf("IsTrackerDown(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestIsUnregistered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		msg    string
		custom []string
		want   bool
	}{
		{"empty", "", nil, false},
		{"exact unregistered", "Unregistered torrent", nil, true},
		{"torrent not found", "Torrent not found", nil, true},
		{"infohash not found", "Infohash not found", nil, true},
		{"german torrent nicht gefunden", "Torrent nicht gefunden", nil, true},
		{"case insensitive", "TORRENT HAS BEEN DELETED", nil, true},
		{"trump pattern", "this is trump", nil, true},
		{"working msg not matched", "Working", nil, false},
		{"empty custom passes default", "Unregistered", []string{}, true},
		{"custom russian extends", "Раздача погашена", []string{"Раздача погашена"}, true},
		{"custom case insensitive", "раздача погашена", []string{"Раздача Погашена"}, true},
		{"custom empty entry skipped", "Unregistered", []string{""}, true},
		{"custom no match still tries defaults", "Torrent not found", []string{"foobar"}, true},
		{"precedence: tracker down beats unregistered", "Service Unavailable: torrent has been deleted", nil, false},
		{"precedence: timeout overrides unregistered substring", "timeout (uploaded)", nil, false},
		{"unknown class neutral", "Tracker returned weird thing", nil, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsUnregistered(tc.msg, tc.custom)
			if got != tc.want {
				t.Fatalf("IsUnregistered(%q, %v) = %v, want %v", tc.msg, tc.custom, got, tc.want)
			}
		})
	}
}

func TestPatternListsNonEmpty(t *testing.T) {
	t.Parallel()
	// Guard against accidental clobber of the embedded lists.
	if len(defaultUnregisteredStatuses) < 20 {
		t.Fatalf("defaultUnregisteredStatuses unexpectedly short: %d", len(defaultUnregisteredStatuses))
	}
	if len(trackerDownStatuses) < 15 {
		t.Fatalf("trackerDownStatuses unexpectedly short: %d", len(trackerDownStatuses))
	}
}
