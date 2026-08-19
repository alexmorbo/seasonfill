package domain

import (
	"fmt"
	"strings"
)

// Section is the value-object identifier of one enrichment/render section of a
// media detail page. It is the ADR-0022 CANONICAL cross-type enum — the SAME
// eight sections describe both series and movie; per-type differences live in
// the plugin adapters, never in this enum. Comparable struct (usable as a map
// key); the sole valid non-zero instances are the exported constants below or a
// value from ParseSection. Zero value is invalid.
type Section struct {
	value string
}

// Unexported backing literals — the canonical section names.
const (
	sectionText       = "text"       // title / overview localized text
	sectionCast       = "cast"       // people credits (shared people_texts)
	sectionRecs       = "recs"       // recommendations rail
	sectionMedia      = "media"      // posters / backdrops / logos
	sectionKeywords   = "keywords"   // keyword taxonomy
	sectionSeasons    = "seasons"    // seasons/episodes (series-only plugin)
	sectionCollection = "collection" // belongs-to collection (movie-only plugin)
	sectionHero       = "hero"       // skeleton / hero header + sidebar
)

// Exported canonical section constants.
var (
	SectionText       = Section{value: sectionText}
	SectionCast       = Section{value: sectionCast}
	SectionRecs       = Section{value: sectionRecs}
	SectionMedia      = Section{value: sectionMedia}
	SectionKeywords   = Section{value: sectionKeywords}
	SectionSeasons    = Section{value: sectionSeasons}
	SectionCollection = Section{value: sectionCollection}
	SectionHero       = Section{value: sectionHero}
)

// CanonicalSections is the fixed, ordered set of every valid Section. Order is
// the ADR-0022 rollout order (text → cast → recs → media → keywords → seasons →
// collection → hero) and is used by ParseSection's validity check.
var CanonicalSections = []Section{
	SectionText,
	SectionCast,
	SectionRecs,
	SectionMedia,
	SectionKeywords,
	SectionSeasons,
	SectionCollection,
	SectionHero,
}

// LegacySeriesSectionMap documents how the existing series freshener section
// names (internal/seriesdetail/app/freshener.Section) map onto the canonical
// engine sections. It is DOCUMENTATION for the later series plugin stories —
// NOT consumed by S1 code. Notably: series "overview" → text, "recommendations"
// → recs, "skeleton" → hero.
var LegacySeriesSectionMap = map[string]Section{
	"overview":        SectionText,
	"cast":            SectionCast,
	"recommendations": SectionRecs,
	"media":           SectionMedia,
	"skeleton":        SectionHero,
}

// ParseSection normalizes (trim + lowercase) and validates s against the
// canonical enum. Unknown input → ErrInvalidSection.
func ParseSection(s string) (Section, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	for _, c := range CanonicalSections {
		if c.value == norm {
			return c, nil
		}
	}
	return Section{}, fmt.Errorf("%w: got %q", ErrInvalidSection, s)
}

// String returns the canonical section name, or "" for the zero value.
func (s Section) String() string { return s.value }

// IsZero reports the invalid zero value.
func (s Section) IsZero() bool { return s.value == "" }

// Valid reports whether s is a known canonical section.
func (s Section) Valid() bool {
	for _, c := range CanonicalSections {
		if c.value == s.value {
			return true
		}
	}
	return false
}
