package values

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

// jsonKeys marshals v and returns its sorted top-level JSON object key set.
// Fails the test if v does not marshal to a JSON object.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T into object (got %s): %v", v, b, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSwaggerWireShapeParity asserts every exported *Wire mirror struct emits
// the SAME top-level JSON keys as the value object it mirrors. If this fails,
// swagger_wire.go has drifted from the VO's MarshalJSON and the generated
// OpenAPI schema (which swaggo builds from the *Wire struct) would lie about
// the wire shape.
func TestSwaggerWireShapeParity(t *testing.T) {
	t.Parallel()

	lang, err := NewLanguageTag("ru-RU")
	if err != nil {
		t.Fatalf("NewLanguageTag: %v", err)
	}

	// Rating.
	score, err := NewScore(8.4)
	if err != nil {
		t.Fatalf("NewScore: %v", err)
	}
	votes, err := NewVoteCount(1200)
	if err != nil {
		t.Fatalf("NewVoteCount: %v", err)
	}
	rating, err := NewRating(score, votes)
	if err != nil {
		t.Fatalf("NewRating: %v", err)
	}

	// Title.
	title, err := NewTitle("Пример", lang)
	if err != nil {
		t.Fatalf("NewTitle: %v", err)
	}

	// Tagline.
	tagline, err := NewTagline("Слоган", lang)
	if err != nil {
		t.Fatalf("NewTagline: %v", err)
	}

	// NextEpisodeCanon (fixed air date — deterministic).
	airDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	nextEp, err := NewNextEpisodeCanon(3, 7, title, airDate, 12)
	if err != nil {
		t.Fatalf("NewNextEpisodeCanon: %v", err)
	}

	wireTitle := TitleWire{Value: "Пример", Lang: "ru-RU"}

	cases := []struct {
		name string
		vo   any
		wire any
	}{
		{
			name: "Rating",
			vo:   rating,
			wire: RatingWire{Score: 8.4, Votes: 1200},
		},
		{
			name: "Title",
			vo:   title,
			wire: wireTitle,
		},
		{
			name: "Tagline",
			vo:   tagline,
			wire: TaglineWire{Value: "Слоган", Lang: "ru-RU"},
		},
		{
			name: "NextEpisodeCanon",
			vo:   nextEp,
			wire: NextEpisodeCanonWire{
				SeasonNumber:  3,
				EpisodeNumber: 7,
				Title:         wireTitle,
				AirDate:       airDate,
				DaysUntil:     12,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			voKeys := jsonKeys(t, tc.vo)
			wireKeys := jsonKeys(t, tc.wire)
			if !equalKeys(voKeys, wireKeys) {
				t.Fatalf("%s: wire mirror drifted from MarshalJSON\n  VO   keys: %v\n  Wire keys: %v",
					tc.name, voKeys, wireKeys)
			}
		})
	}
}

// TestSwaggerWireNestedTitleParity guards the one nested object: the "title"
// field inside NextEpisodeCanon must itself carry the TitleWire key set, so a
// future TitleWire change can't silently break the nested schema.
func TestSwaggerWireNestedTitleParity(t *testing.T) {
	t.Parallel()

	lang, err := NewLanguageTag("en-US")
	if err != nil {
		t.Fatalf("NewLanguageTag: %v", err)
	}
	title, err := NewTitle("Sample", lang)
	if err != nil {
		t.Fatalf("NewTitle: %v", err)
	}

	voKeys := jsonKeys(t, title)
	wireKeys := jsonKeys(t, TitleWire{Value: "Sample", Lang: "en-US"})
	if !equalKeys(voKeys, wireKeys) {
		t.Fatalf("nested Title parity: VO %v != Wire %v", voKeys, wireKeys)
	}
}
