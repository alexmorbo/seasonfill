package enrichment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// img builds a TVImage with an optional iso_639_1 (nil for language-agnostic).
func img(path string, iso *string, voteAvg float64, voteCount int) tmdb.TVImage {
	return tmdb.TVImage{
		FilePath:    path,
		ISO6391:     iso,
		VoteAverage: voteAvg,
		VoteCount:   voteCount,
	}
}

func TestShortLang(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"en-US", "en"},
		{"ru-RU", "ru"},
		{"en", "en"},
		{"EN-us", "en"},
		{"pt-BR", "pt"},
		{"", ""},
		{"zh-Hans-CN", "zh"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, shortLang(c.in), "shortLang(%q)", c.in)
	}
}

func TestPickPosterForLang(t *testing.T) {
	t.Parallel()
	en := new("en")
	ru := new("ru")

	full := &tmdb.TVImages{Posters: []tmdb.TVImage{
		img("/en_high.jpg", en, 8.1, 40),
		img("/en_low.jpg", en, 5.0, 3),
		img("/ru_ferma.jpg", ru, 7.2, 11),
		img("/neutral_a.jpg", nil, 6.0, 9),
		img("/neutral_b.jpg", nil, 6.0, 25),
	}}

	tests := []struct {
		name string
		imgs *tmdb.TVImages
		lang string
		want *string
	}{
		{"nil imgs", nil, "en-US", nil},
		{"empty posters", &tmdb.TVImages{Posters: nil}, "en-US", nil},
		{"en picks highest-vote en", full, "en-US", new("/en_high.jpg")},
		{"ru picks ru tier first", full, "ru-RU", new("/ru_ferma.jpg")},
		{
			"missing lang falls to nil tier, vote_count tie-break",
			full, "de-DE", new("/neutral_b.jpg"),
		},
		{
			"only-ru entries, ask en → falls through to en tier absent, nil absent → nil",
			&tmdb.TVImages{Posters: []tmdb.TVImage{img("/ru_only.jpg", ru, 9.0, 5)}},
			"en-US", nil,
		},
		{
			"only-null entries → nil tier wins for any lang",
			&tmdb.TVImages{Posters: []tmdb.TVImage{img("/only_null.jpg", nil, 4.0, 2)}},
			"ru-RU", new("/only_null.jpg"),
		},
		{
			"empty lang → nil tier then en",
			full, "", new("/neutral_b.jpg"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickPosterForLang(tt.imgs, tt.lang)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}

func TestPickBackdropForLang(t *testing.T) {
	t.Parallel()
	en := new("en")
	ru := new("ru")

	full := &tmdb.TVImages{Backdrops: []tmdb.TVImage{
		img("/neutral_bd.jpg", nil, 5.5, 12),
		img("/en_bd.jpg", en, 7.0, 30),
		img("/ru_bd.jpg", ru, 8.0, 20),
	}}

	tests := []struct {
		name string
		imgs *tmdb.TVImages
		lang string
		want *string
	}{
		{"nil imgs", nil, "en-US", nil},
		{"empty backdrops", &tmdb.TVImages{Backdrops: nil}, "ru-RU", nil},
		{"neutral first even for en", full, "en-US", new("/neutral_bd.jpg")},
		{"neutral first even for ru", full, "ru-RU", new("/neutral_bd.jpg")},
		{
			"no neutral → falls to requested lang",
			&tmdb.TVImages{Backdrops: []tmdb.TVImage{
				img("/en_bd.jpg", en, 7.0, 30),
				img("/ru_bd.jpg", ru, 8.0, 20),
			}},
			"ru-RU", new("/ru_bd.jpg"),
		},
		{
			"no neutral, no requested → en",
			&tmdb.TVImages{Backdrops: []tmdb.TVImage{img("/en_bd.jpg", en, 7.0, 30)}},
			"ru-RU", new("/en_bd.jpg"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickBackdropForLang(tt.imgs, tt.lang)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}

func TestPickCanonicalPoster(t *testing.T) {
	t.Parallel()
	en := new("en")
	ru := new("ru")

	tests := []struct {
		name string
		imgs *tmdb.TVImages
		want *string
	}{
		{"nil imgs", nil, nil},
		{"empty", &tmdb.TVImages{Posters: nil}, nil},
		{
			"nil tier first with vote_count tie-break",
			&tmdb.TVImages{Posters: []tmdb.TVImage{
				img("/en_high.jpg", en, 9.0, 100),
				img("/neutral_a.jpg", nil, 6.0, 9),
				img("/neutral_b.jpg", nil, 6.0, 25),
			}},
			new("/neutral_b.jpg"),
		},
		{
			"no neutral → en",
			&tmdb.TVImages{Posters: []tmdb.TVImage{
				img("/ru.jpg", ru, 9.0, 5),
				img("/en.jpg", en, 4.0, 2),
			}},
			new("/en.jpg"),
		},
		{
			"only ru → nil (canon never returns lang-specific)",
			&tmdb.TVImages{Posters: []tmdb.TVImage{img("/ru.jpg", ru, 9.0, 5)}},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickCanonicalPoster(tt.imgs)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}

func TestPickCanonicalBackdrop(t *testing.T) {
	t.Parallel()
	en := new("en")

	got := pickCanonicalBackdrop(&tmdb.TVImages{Backdrops: []tmdb.TVImage{
		img("/neutral.jpg", nil, 5.0, 3),
		img("/en.jpg", en, 9.0, 99),
	}})
	require.NotNil(t, got)
	assert.Equal(t, "/neutral.jpg", *got)

	assert.Nil(t, pickCanonicalBackdrop(nil))
	assert.Nil(t, pickCanonicalBackdrop(&tmdb.TVImages{Backdrops: nil}))
}

// TestPickPoster_EmptyFilePath_NilNotPanic guards the malformed-row branch:
// a winning entry with an empty file_path returns nil (root fallback), not "".
func TestPickPoster_EmptyFilePath_Nil(t *testing.T) {
	t.Parallel()
	got := pickPosterForLang(&tmdb.TVImages{Posters: []tmdb.TVImage{
		img("", new("en"), 9.0, 5),
	}}, "en-US")
	assert.Nil(t, got)
}

// TestPickPosterForLangRooted_KeyAndPeele reproduces #1020: series 373 "Key &
// Peele" (tmdb 43082). A Russian-art poster is community-mis-tagged upstream and
// carries the HIGHEST vote_count, so the plain vote ranking pulls it into the
// en-US tier and poisons the base series_media_texts row. The root-anchored
// picker must return TMDB's curated en primary (rootPoster) instead, while the
// ru row is unaffected. Also locks the "don't over-filter" fallback.
func TestPickPosterForLangRooted_KeyAndPeele(t *testing.T) {
	t.Parallel()
	en := new("en")
	ru := new("ru")

	const rootEN = "/kp_en_primary.jpg" // TMDB /tv/43082?language=en-US poster_path

	// Mis-tag tagged en: it sits IN the en tier alongside the genuine primary
	// and wins the plain vote ranking (avg tie 6.0, count 950 ≫ 8).
	enTagged := &tmdb.TVImages{Posters: []tmdb.TVImage{
		img(rootEN, en, 6.0, 8),               // genuine en primary (== root)
		img("/kp_ru_art.jpg", en, 6.0, 950),   // Russian art MIS-TAGGED en, high votes
		img("/kp_ru_poster.jpg", ru, 7.0, 40), // genuine ru poster
	}}

	// Mis-tag tagged null: the exact-en tier already excludes it, but the root
	// anchor must still land on the primary (belt-and-suspenders).
	nullTagged := &tmdb.TVImages{Posters: []tmdb.TVImage{
		img(rootEN, en, 6.0, 8),
		img("/kp_ru_art.jpg", nil, 9.0, 999), // Russian art MIS-TAGGED null, highest votes
		img("/kp_ru_poster.jpg", ru, 7.0, 40),
	}}

	// AUDIT-S1 / F-01: the mis-tag is tagged "en" (so it OCCUPIES the exact-en
	// tier) with the highest votes, while TMDB's editorial primary is a NULL-iso
	// poster living in a LATER tier. The exact-en tier is non-empty but holds
	// ONLY the mis-tag, so the old winning-tier-only anchor never reaches the null
	// tier where the primary lives → the mis-tag poisons the en-US row. The
	// all-tiers anchor must still return the primary. RED before the fix.
	const rootNull = "/kp_null_primary.jpg" // TMDB /tv/43082?language=en-US primary, NULL-iso
	enTierMistagNullPrimary := &tmdb.TVImages{Posters: []tmdb.TVImage{
		img("/kp_ru_art.jpg", en, 6.0, 950), // Russian art MIS-TAGGED en, IN exact tier, top votes
		img(rootNull, nil, 4.0, 3),          // editorial primary, NULL-iso (== root), low votes
		img("/kp_ru_poster.jpg", ru, 7.0, 40),
	}}

	t.Run("en-tagged mis-tag: root primary wins, NOT the high-vote mis-tag", func(t *testing.T) {
		got := pickPosterForLangRooted(enTagged, "en-US", rootEN)
		require.NotNil(t, got)
		assert.Equal(t, rootEN, *got)
		// Proof the anchor is load-bearing: without it the mis-tag wins the tier.
		unanchored := pickPosterForLang(enTagged, "en-US")
		require.NotNil(t, unanchored)
		assert.Equal(t, "/kp_ru_art.jpg", *unanchored, "sanity: plain vote ranking picks the mis-tag")
	})

	// NON-load-bearing: the primary is tagged en and already sits in the winning
	// exact-en tier, so this passes even with the anchor deleted. Kept as a
	// belt-and-suspenders guard; the load-bearing case is the subtest below.
	t.Run("null-tagged mis-tag, en-tagged primary: root wins (non-load-bearing)", func(t *testing.T) {
		got := pickPosterForLangRooted(nullTagged, "en-US", rootEN)
		require.NotNil(t, got)
		assert.Equal(t, rootEN, *got)
	})

	t.Run("en-tier mis-tag + null-iso primary: primary wins across tiers (AUDIT-S1, RED pre-fix)", func(t *testing.T) {
		got := pickPosterForLangRooted(enTierMistagNullPrimary, "en-US", rootNull)
		require.NotNil(t, got)
		assert.Equal(t, rootNull, *got)
		// Proof this fixture is load-bearing: the plain vote ranking (no anchor)
		// returns the mis-tag, because the exact-en tier is non-empty and holds
		// only the mis-tag — so the old winning-tier-only anchor could never win.
		unanchored := pickPosterForLang(enTierMistagNullPrimary, "en-US")
		require.NotNil(t, unanchored)
		assert.Equal(t, "/kp_ru_art.jpg", *unanchored, "sanity: plain vote ranking picks the mis-tag")
	})

	t.Run("ru row still resolves to the ru poster (strict, unaffected)", func(t *testing.T) {
		for _, imgs := range []*tmdb.TVImages{enTagged, nullTagged} {
			got := pickPosterForLangStrict(imgs, "ru-RU")
			require.NotNil(t, got)
			assert.Equal(t, "/kp_ru_poster.jpg", *got)
		}
	})
}

// TestPickPosterForLangRooted_Semantics covers the non-Key&Peele branches:
// a genuine per-language poster is still honoured when the root primary is not
// among the tier candidates, and the picker never over-filters to empty.
func TestPickPosterForLangRooted_Semantics(t *testing.T) {
	t.Parallel()
	en := new("en")

	tests := []struct {
		name       string
		imgs       *tmdb.TVImages
		lang       string
		rootPoster string
		want       *string
	}{
		{
			// Root not among the tier candidates → keep the vote-ranked pick
			// (honour a genuinely-correct, higher-voted per-language poster).
			name: "root absent from tier → vote ranking honoured",
			imgs: &tmdb.TVImages{Posters: []tmdb.TVImage{
				img("/a.jpg", en, 7.0, 10),
				img("/b.jpg", en, 8.0, 20),
			}},
			lang: "en-US", rootPoster: "/not_in_posters.jpg", want: new("/b.jpg"),
		},
		{
			// Regression: ONLY a null high-vote image, no exact-lang, empty root
			// → still return the null image (do NOT over-filter to empty).
			name: "null-only, no root → still returns the null image",
			imgs: &tmdb.TVImages{Posters: []tmdb.TVImage{
				img("/only_null.jpg", nil, 9.0, 900),
			}},
			lang: "en-US", rootPoster: "", want: new("/only_null.jpg"),
		},
		{
			// No posters at all but TMDB has a primary → fall back to root.
			name: "empty posters → root fallback",
			imgs: &tmdb.TVImages{Posters: nil},
			lang: "en-US", rootPoster: "/root_only.jpg", want: new("/root_only.jpg"),
		},
		{
			// No posters and no root → nil.
			name: "empty posters, no root → nil",
			imgs: nil, lang: "en-US", rootPoster: "", want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickPosterForLangRooted(tt.imgs, tt.lang, tt.rootPoster)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}
