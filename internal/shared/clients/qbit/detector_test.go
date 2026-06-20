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
func (f *fakeClient) NewSyncSession(ctx context.Context) (SyncSession, error) {
	return nil, errors.New("fakeClient: NewSyncSession not implemented")
}
func (f *fakeClient) Close() error { return nil }

func TestDetector_Detect(t *testing.T) {
	t.Parallel()
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
			name: "all not-working with tracker-down msg → TrackerDown",
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 4, Msg: "Service Unavailable"},
			},
			wantDown:     true,
			wantTrkURL:   "http://tr1/announce",
			wantTrackMsg: "Service Unavailable",
		},
		{
			name: "mixed not-working: unregistered wins over tracker-down (inter-tracker)",
			trackers: []Tracker{
				{URL: "http://tr1/announce", Status: 4, Msg: "Torrent not found"},
				{URL: "http://tr2/announce", Status: 4, Msg: "Tracker is down"},
			},
			wantUnreg:    true,
			wantTrkURL:   "http://tr1/announce",
			wantTrackMsg: "Torrent not found",
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
	t.Parallel()
	fc := &fakeClient{}
	d := NewDetector(fc, nil)
	_, err := d.Detect(context.Background(), "")
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestDetector_PropagatesClientError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()
			got := isSyntheticTracker(tc.url)
			if got != tc.want {
				t.Fatalf("isSyntheticTracker(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// TestDetect_UnregisteredWinsOverTrackerDownAcrossTrackers is the exact
// live Y.F.A.N. S02 (hash 10b5dcf4…) repro: rutracker has 4 mirror
// announces. One mirror (bt2) returns "520 (unknown HTTP error)" which
// matches IsTrackerDown via the "(unknown http error)" substring; the
// other three (bt, bt3, bt4) return the definite "Torrent not
// registered" rejection. Semantically rutracker is ONE tracker — any
// single mirror confirming the rejection is authoritative regardless
// of another mirror being transiently down. After the inter-tracker
// precedence flip, Unregistered wins and TrackerURL points at the
// first unregistered tracker in list order (bt).
func TestDetect_UnregisteredWinsOverTrackerDownAcrossTrackers(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "http://bt.t-ru.org/ann?magnet", Status: 5, Msg: "Torrent not registered"},
			{URL: "http://bt2.t-ru.org/ann?magnet", Status: 4, Msg: "520 (unknown HTTP error)"},
			{URL: "http://bt3.t-ru.org/ann?magnet", Status: 5, Msg: "Torrent not registered"},
			{URL: "http://bt4.t-ru.org/ann?magnet", Status: 5, Msg: "Torrent not registered"},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !res.Unregistered {
		t.Fatalf("want Unregistered=true (any clean unreg mirror is authoritative), got %+v", res)
	}
	if res.TrackerDown {
		t.Fatalf("want TrackerDown=false (unregistered wins inter-tracker), got %+v", res)
	}
	if res.TrackerURL != "http://bt.t-ru.org/ann?magnet" {
		t.Fatalf("want first unregistered mirror URL, got %q", res.TrackerURL)
	}
	if res.TrackerMsg != "Torrent not registered" {
		t.Fatalf("want unregistered msg, got %q", res.TrackerMsg)
	}
}

// TestDetect_TrackerDownStillWinsWhenNoUnregistered confirms that with
// the precedence flip, TrackerDown still fires as the verdict when no
// tracker matches IsUnregistered. All four trackers carry distinct
// tracker-down patterns; none match the unregistered list. Pass 1 is
// empty-handed → Pass 2 returns TrackerDown on the first matching
// tracker in list order.
func TestDetect_TrackerDownStillWinsWhenNoUnregistered(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "http://tr1/announce", Status: 4, Msg: "Service Unavailable"},
			{URL: "http://tr2/announce", Status: 4, Msg: "Tracker is down"},
			{URL: "http://tr3/announce", Status: 4, Msg: "Connection timeout"},
			{URL: "http://tr4/announce", Status: 4, Msg: "Bad Gateway"},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Unregistered {
		t.Fatalf("want Unregistered=false (no unreg pattern matched), got %+v", res)
	}
	if !res.TrackerDown {
		t.Fatalf("want TrackerDown=true (all trackers down), got %+v", res)
	}
	if res.TrackerURL != "http://tr1/announce" {
		t.Fatalf("want first tracker URL, got %q", res.TrackerURL)
	}
	if res.TrackerMsg != "Service Unavailable" {
		t.Fatalf("want first tracker msg, got %q", res.TrackerMsg)
	}
}

// TestDetect_SingleAmbiguousMessageStillNotUnregistered validates that
// the intra-message precedence guard inside IsUnregistered
// (patterns.go:114-116) is unchanged. A single tracker carries a
// message that contains BOTH a tracker-down substring ("internal
// server error") and an unregistered substring ("uploaded"). The
// guard makes IsUnregistered return false → Pass 1 is empty-handed →
// Pass 2 returns TrackerDown. This confirms the inter-tracker flip
// does not break per-message resolution.
func TestDetect_SingleAmbiguousMessageStillNotUnregistered(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "http://tr1/announce", Status: 4, Msg: "Internal Server Error: uploaded body too large"},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Unregistered {
		t.Fatalf("want Unregistered=false (intra-message guard), got %+v", res)
	}
	if !res.TrackerDown {
		t.Fatalf("want TrackerDown=true (intra-message guard resolves to down), got %+v", res)
	}
	if res.TrackerURL != "http://tr1/announce" {
		t.Fatalf("want tr1 URL, got %q", res.TrackerURL)
	}
}

// TestDetect_TwoTrackerDownOneAmbiguous confirms Pass 1 is correctly
// empty-handed when no tracker carries a clean unregistered message —
// even when one message is ambiguous (contains an unregistered
// substring guarded by intra-message precedence). All three messages
// resolve to tracker-down via IsTrackerDown; none match IsUnregistered.
// Pass 2 fires and returns TrackerDown on the first matching tracker.
func TestDetect_TwoTrackerDownOneAmbiguous(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{trackersByHash: map[string][]Tracker{
		"H": {
			{URL: "http://tr1/announce", Status: 4, Msg: "Tracker is down"},
			{URL: "http://tr2/announce", Status: 4, Msg: "Internal Server Error: uploaded body too large"},
			{URL: "http://tr3/announce", Status: 4, Msg: "Connection refused"},
		},
	}}
	d := NewDetector(fc, nil)
	res, err := d.Detect(context.Background(), "H")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Unregistered {
		t.Fatalf("want Unregistered=false (no clean unreg in any tracker), got %+v", res)
	}
	if !res.TrackerDown {
		t.Fatalf("want TrackerDown=true (pass 2 fires after empty pass 1), got %+v", res)
	}
	if res.TrackerURL != "http://tr1/announce" {
		t.Fatalf("want first matching tracker URL, got %q", res.TrackerURL)
	}
	if res.TrackerMsg != "Tracker is down" {
		t.Fatalf("want first matching tracker msg, got %q", res.TrackerMsg)
	}
}
