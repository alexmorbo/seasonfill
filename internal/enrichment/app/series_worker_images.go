package enrichment

import (
	"sort"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// Per-language poster/backdrop selection from the already-fetched
// tv.Images payload (TMDB append_to_response=images). Fixes #977: the EN
// grid used to render the Russian «Ферма» poster because every language
// wrote the SAME root poster_path. TMDB tags each images.posters[] /
// images.backdrops[] entry with iso_639_1, so the localised writer can pick
// art in the requested language instead of the single root path.
//
// Priority chains (first non-empty group wins; within a group order by
// VoteAverage desc, tie-break VoteCount desc, take the top FilePath):
//
//   - pickPosterForLang:   short(lang) → language-agnostic (nil) → "en"
//     Posters frequently carry localised title text baked into the art, so
//     the requested language is preferred first; a language-agnostic (no
//     text) poster beats an English one when the requested language is
//     absent.
//   - pickBackdropForLang: language-agnostic (nil) → short(lang) → "en"
//     Backdrops are usually textless key-art — prefer neutral first.
//   - pickCanonicalPoster / pickCanonicalBackdrop: nil → "en"
//     Language-agnostic canon paths (A4 series canon, prewarm, canon-guard).
//
// All functions are pure: no DB, no I/O, no external deps beyond the tmdb
// types. They return a non-nil *string ONLY for a non-empty FilePath; nil
// signals the caller to fall back to the root tv.PosterPath / tv.BackdropPath.

// shortLang returns the primary subtag of a BCP-47 tag, lowercased:
// "en-US" → "en", "ru-RU" → "ru", "" → "". Used to match TMDB's
// iso_639_1 (always a bare 2-letter code) against a full user language tag.
func shortLang(lang string) string {
	if i := strings.IndexByte(lang, '-'); i >= 0 {
		lang = lang[:i]
	}
	return strings.ToLower(lang)
}

// pickPosterForLang: short(lang) → nil (language-agnostic) → "en".
func pickPosterForLang(imgs *tmdb.TVImages, lang string) *string {
	if imgs == nil || len(imgs.Posters) == 0 {
		return nil
	}
	return pickByLangPriority(imgs.Posters, langPriority(shortLang(lang)))
}

// pickBackdropForLang: nil (language-agnostic) → short(lang) → "en".
func pickBackdropForLang(imgs *tmdb.TVImages, lang string) *string {
	if imgs == nil || len(imgs.Backdrops) == 0 {
		return nil
	}
	return pickByLangPriority(imgs.Backdrops, neutralFirstPriority(shortLang(lang)))
}

// pickCanonicalPoster: nil (language-agnostic) → "en".
func pickCanonicalPoster(imgs *tmdb.TVImages) *string {
	if imgs == nil || len(imgs.Posters) == 0 {
		return nil
	}
	return pickByLangPriority(imgs.Posters, canonicalPriority())
}

// pickCanonicalBackdrop: nil (language-agnostic) → "en".
func pickCanonicalBackdrop(imgs *tmdb.TVImages) *string {
	if imgs == nil || len(imgs.Backdrops) == 0 {
		return nil
	}
	return pickByLangPriority(imgs.Backdrops, canonicalPriority())
}

// langMatcher matches a TVImage's iso_639_1 for one priority tier.
type langMatcher func(iso *string) bool

// langPriority: exact short(lang) → language-agnostic (nil) → "en".
// The short == "" case (empty/invalid lang) collapses to nil → "en".
func langPriority(short string) []langMatcher {
	if short == "" {
		return []langMatcher{matchNil, matchISO("en")}
	}
	return []langMatcher{matchISO(short), matchNil, matchISO("en")}
}

// neutralFirstPriority: language-agnostic (nil) → short(lang) → "en".
func neutralFirstPriority(short string) []langMatcher {
	if short == "" {
		return []langMatcher{matchNil, matchISO("en")}
	}
	return []langMatcher{matchNil, matchISO(short), matchISO("en")}
}

// canonicalPriority: language-agnostic (nil) → "en".
func canonicalPriority() []langMatcher {
	return []langMatcher{matchNil, matchISO("en")}
}

func matchNil(iso *string) bool { return iso == nil }

func matchISO(want string) langMatcher {
	return func(iso *string) bool {
		return iso != nil && strings.ToLower(*iso) == want
	}
}

// pickByLangPriority walks the priority tiers in order; the first tier with
// at least one matching image wins. Within that tier the entries are ordered
// by VoteAverage desc, tie-broken by VoteCount desc, and the top FilePath is
// returned (nil if empty).
func pickByLangPriority(imgs []tmdb.TVImage, tiers []langMatcher) *string {
	for _, match := range tiers {
		var group []tmdb.TVImage
		for _, img := range imgs {
			if match(img.ISO6391) {
				group = append(group, img)
			}
		}
		if len(group) == 0 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].VoteAverage != group[j].VoteAverage {
				return group[i].VoteAverage > group[j].VoteAverage
			}
			return group[i].VoteCount > group[j].VoteCount
		})
		if fp := group[0].FilePath; fp != "" {
			return &fp
		}
		return nil
	}
	return nil
}

// pickSeasonPosterForLang: full priority chain (short(lang) → language-agnostic
// (nil) → "en"), used for the CALL language. Mirrors pickPosterForLang but over
// SeasonImages (posters only). nil → caller falls back to the root season
// poster_path.
func pickSeasonPosterForLang(imgs *tmdb.SeasonImages, lang string) *string {
	if imgs == nil || len(imgs.Posters) == 0 {
		return nil
	}
	return pickByLangPriority(imgs.Posters, langPriority(shortLang(lang)))
}

// pickSeasonPosterStrict: per-language tier ONLY (matchISO(short(lang)) — no
// agnostic, no "en", no root fallback). Used for NON-call languages so the en-US
// season-poster tier is never poisoned by call-lang or English art: a non-call
// row is written only when TMDB actually carries a poster tagged in that EXACT
// language. Empty short(lang) → nil.
func pickSeasonPosterStrict(imgs *tmdb.SeasonImages, lang string) *string {
	if imgs == nil || len(imgs.Posters) == 0 || shortLang(lang) == "" {
		return nil
	}
	return pickByLangPriority(imgs.Posters, []langMatcher{matchISO(shortLang(lang))})
}

// pickPosterForLangStrict: EXACT short(lang) tier ONLY (no agnostic/en/root).
// Series analogue of pickSeasonPosterStrict. Used for NON-base languages in the
// A4 series_media_texts writer so the base tier is never poisoned by en art: a
// non-base row is written only when TMDB actually carries a poster tagged in
// that EXACT language. Empty short(lang) → nil.
func pickPosterForLangStrict(imgs *tmdb.TVImages, lang string) *string {
	if imgs == nil || len(imgs.Posters) == 0 || shortLang(lang) == "" {
		return nil
	}
	return pickByLangPriority(imgs.Posters, []langMatcher{matchISO(shortLang(lang))})
}

// pickBackdropForLangStrict: EXACT short(lang) tier ONLY (no agnostic/en/root).
// Non-base backdrop counterpart to pickPosterForLangStrict.
func pickBackdropForLangStrict(imgs *tmdb.TVImages, lang string) *string {
	if imgs == nil || len(imgs.Backdrops) == 0 || shortLang(lang) == "" {
		return nil
	}
	return pickByLangPriority(imgs.Backdrops, []langMatcher{matchISO(shortLang(lang))})
}
