package qbit

import (
	"context"
	"errors"
	"testing"
)

// fakeClient implements Client with table-driven tracker payloads.
type fakeClient struct {
	trackersByHash map[string][]Tracker
	err            error
}

func (f *fakeClient) Login(ctx context.Context) error { return nil }
func (f *fakeClient) ListTorrents(ctx context.Context) ([]Torrent, error) {
	return nil, nil
}
func (f *fakeClient) GetTrackers(ctx context.Context, hash string) ([]Tracker, error) {
	if f.err != nil {
		return nil, f.err
	}
	trk, ok := f.trackersByHash[hash]
	if !ok {
		return nil, ErrTorrentNotFound
	}
	return trk, nil
}
func (f *fakeClient) Ping(ctx context.Context) error { return nil }
func (f *fakeClient) Close() error                   { return nil }

func TestDetector_Detect(t *testing.T) {
	cases := []struct {
		name         string
		trackers     []Tracker
		custom       []string
		wantUnreg    bool
		wantDown     bool
		wantTrkURL   string
		wantTrackMsg string
	}{
		{
			name:     "empty",
			trackers: []Tracker{},
		},
		{
			name: "only disabled (dht/pex)",
			trackers: []Tracker{
				{URL: "** [DHT] **", Status: 0, Msg: ""},
				{URL: "** [PeX] **", Status: 0, Msg: ""},
			},
		},
		{
			name: "single working tracker — alive",
			trackers: []Tracker{
				{URL: "http://tr/announce", Status: 2, Msg: ""},
			},
		},
		{
			name: "C-4: working + not-working with unregistered msg — alive",
			trackers: []Tracker{
				{URL: "http://tr-down/announce", Status: 4, Msg: "Torrent not found"},
				{URL: "http://tr-up/announce", Status: 2, Msg: ""},
			},
		},
		{
			name: "C-4: working + tracker-down — alive",
			trackers: []Tracker{
				{URL: "http://tr-down/announce", Status: 4, Msg: "Tracker is down"},
				{URL: "http://tr-up/announce", Status: 2, Msg: ""},
			},
		},
		{
			name: "all not-working with unregistered msg → Unregistered",
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 4, Msg: "Torrent not found"},
			},
			wantUnreg:    true,
			wantTrkURL:   "http://tr1/announce",
			wantTrackMsg: "Torrent not found",
		},
		{
			name: "all not-working with tracker-down msg → TrackerDown (precedence)",
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 4, Msg: "Service Unavailable"},
			},
			wantDown:     true,
			wantTrkURL:   "http://tr1/announce",
			wantTrackMsg: "Service Unavailable",
		},
		{
			name: "mixed not-working: tracker-down first wins over unregistered",
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 4, Msg: "Torrent not found"},
				{URL: "http://tr2/announce", Status: 4, Msg: "Tracker is down"},
			},
			wantDown:     true,
			wantTrkURL:   "http://tr2/announce",
			wantTrackMsg: "Tracker is down",
		},
		{
			name: "all not-working unknown msg → neutral",
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 4, Msg: "Some weird tracker thing"},
			},
		},
		{
			name: "custom russian msg matches",
			trackers: []Tracker{
				{URL: "http://rt/announce", Status: 4, Msg: "Раздача погашена"},
			},
			custom:       []string{"Раздача погашена"},
			wantUnreg:    true,
			wantTrkURL:   "http://rt/announce",
			wantTrackMsg: "Раздача погашена",
		},
		{
			name: "disabled trackers ignored even if msg matches",
			trackers: []Tracker{
				{URL: "** [DHT] **", Status: 0, Msg: "Torrent not found"},
			},
		},
		{
			name: "updating + not-working unregistered → still not unregistered (no Working seen, but updating != working)",
			// Updating (3) is treated as "not working" for the C-4 short-circuit
			// purposes — only Working (2) makes the torrent alive. So this case
			// behaves as "all non-working" and yields Unregistered=true.
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 3, Msg: ""},
				{URL: "http://tr2/announce", Status: 4, Msg: "Torrent not found"},
			},
			wantUnreg:    true,
			wantTrkURL:   "http://tr2/announce",
			wantTrackMsg: "Torrent not found",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{trackersByHash: map[string][]Tracker{"H": tc.trackers}}
			d := NewDetector(fc, tc.custom)
			res, err := d.Detect(context.Background(), "H")
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if res.Unregistered != tc.wantUnreg {
				t.Fatalf("Unregistered: want %v, got %v", tc.wantUnreg, res.Unregistered)
			}
			if res.TrackerDown != tc.wantDown {
				t.Fatalf("TrackerDown: want %v, got %v", tc.wantDown, res.TrackerDown)
			}
			if tc.wantTrkURL != "" && res.TrackerURL != tc.wantTrkURL {
				t.Fatalf("TrackerURL: want %q, got %q", tc.wantTrkURL, res.TrackerURL)
			}
			if tc.wantTrackMsg != "" && res.TrackerMsg != tc.wantTrackMsg {
				t.Fatalf("TrackerMsg: want %q, got %q", tc.wantTrackMsg, res.TrackerMsg)
			}
		})
	}
}

func TestDetector_EmptyHash(t *testing.T) {
	fc := &fakeClient{}
	d := NewDetector(fc, nil)
	_, err := d.Detect(context.Background(), "")
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestDetector_PropagatesClientError(t *testing.T) {
	fc := &fakeClient{err: errors.New("transport down")}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err == nil || err.Error() != "transport down" {
		t.Fatalf("want transport down error, got %v", err)
	}
	if res.Hash != "H" {
		t.Fatalf("Hash should be set on error path, got %q", res.Hash)
	}
}

func TestDetector_NilCustomList(t *testing.T) {
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {{URL: "http://tr/announce", Status: 4, Msg: "Unregistered"}},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !res.Unregistered {
		t.Fatalf("want Unregistered=true with nil custom list, got %+v", res)
	}
}

// TestDetect_SyntheticDHTDoesNotMaskUnregistered confirms the bug fix:
// qBit reports synthetic DHT/PeX/LSD trackers with Status=Working (2),
// not Status=Disabled (0). Without URL-based filtering, the C-4 short-
// circuit would treat the torrent as alive even though every real
// tracker reports "Torrent not registered". This mirrors the live
// Y.F.A.N. S02 (hash 10b5dcf4…) repro: 3 synthetic entries at
// Status=2 plus 3 real trackers at Status=5 with the unreg message.
func TestDetect_SyntheticDHTDoesNotMaskUnregistered(t *testing.T) {
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "** [DHT] **", Status: 2, Msg: ""},
			{URL: "** [LSD] **", Status: 2, Msg: ""},
			{URL: "** [PeX] **", Status: 2, Msg: ""},
			{URL: "http://bt.t-ru.org/ann?magnet", Status: 5, Msg: "Torrent not registered"},
			{URL: "http://bt2.t-ru.org/ann?magnet", Status: 5, Msg: "Torrent not registered"},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !res.Unregistered {
		t.Fatalf("want Unregistered=true (synthetic trackers must not mask real unreg), got %+v", res)
	}
	if res.TrackerDown {
		t.Fatalf("want TrackerDown=false, got %+v", res)
	}
	if res.TrackerURL != "http://bt.t-ru.org/ann?magnet" {
		t.Fatalf("want first real tracker URL, got %q", res.TrackerURL)
	}
	if res.TrackerMsg != "Torrent not registered" {
		t.Fatalf("want unreg msg, got %q", res.TrackerMsg)
	}
}

// TestDetect_SyntheticPeXAlone covers the degenerate case where qBit
// returns only synthetic entries (no real trackers were ever added).
// After filtering, active is empty so no verdict can be made — must
// return all-false, never crash.
func TestDetect_SyntheticPeXAlone(t *testing.T) {
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "** [DHT] **", Status: 2, Msg: ""},
			{URL: "** [PeX] **", Status: 2, Msg: ""},
			{URL: "** [LSD] **", Status: 2, Msg: ""},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Unregistered || res.TrackerDown {
		t.Fatalf("want all-false (no real trackers to verdict on), got %+v", res)
	}
}

// TestDetect_OneRealWorkingTrackerKeepsItAlive guards the C-4 invariant
// after the synthetic-filter change: when at least one REAL tracker
// reports Working, the torrent is alive even if other real trackers
// report unregistered AND synthetic DHT is present. The synthetic
// filter must not weaken C-4 for real trackers.
func TestDetect_OneRealWorkingTrackerKeepsItAlive(t *testing.T) {
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "** [DHT] **", Status: 2, Msg: ""},
			{URL: "http://tr-up/announce", Status: 2, Msg: ""},
			{URL: "http://tr-down/announce", Status: 4, Msg: "Torrent not registered"},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Unregistered || res.TrackerDown {
		t.Fatalf("want all-false (C-4: real tr-up is Working), got %+v", res)
	}
}

// TestIsSyntheticTracker is a table-driven check of the URL-based
// synthetic filter helper. The three stable strings qBit emits return
// true; anything else (real http/https trackers, empty string,
// case-variant, partial match) returns false. Case-sensitivity is
// intentional — qBit's strings are stable literals.
func TestIsSyntheticTracker(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"** [DHT] **", true},
		{"** [PeX] **", true},
		{"** [LSD] **", true},
		{"", false},
		{"http://tr/announce", false},
		{"https://tracker.example.org:443/announce.php?id=abc", false},
		{"udp://tracker.opentrackr.org:1337/announce", false},
		// case-variants and partial matches are NOT filtered — qBit's
		// strings are stable literals; matching loosely risks dropping
		// real trackers that happen to contain "** [".
		{"** [dht] **", false},
		{"** [DHT]", false},
		{"prefix ** [DHT] **", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.url, func(t *testing.T) {
			got := isSyntheticTracker(tc.url)
			if got != tc.want {
				t.Fatalf("isSyntheticTracker(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
