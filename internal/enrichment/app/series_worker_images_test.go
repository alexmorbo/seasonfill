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
