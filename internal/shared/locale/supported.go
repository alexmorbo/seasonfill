// Package locale exposes the canonical list of BCP-47 language tags the
// product supports in the user-facing UI. The list MUST stay in sync
// with the FE i18next config (`web/src/i18n/index.ts:7`
// `SUPPORTED_LANGS`). When adding a new language:
//
//  1. Add the short code (e.g. "de") to FE SUPPORTED_LANGS + ship
//     `web/src/i18n/locales/de.ts`.
//  2. Add the matching BCP-47 tag (e.g. "de-DE") to
//     SupportedUserLanguages below.
//
// Why a hand-maintained pair instead of FE-derived: the BE binary cannot
// read the FE bundle at runtime (it ships in a separate Helm release),
// and a build-time generator would add complexity for a list that
// currently has two entries. A drift-detection unit test
// (TestSupportedUserLanguages_FEParity) reads the FE source file at
// CI time and fails when the two lists diverge.
package locale

// SupportedUserLanguages is the list the SeriesWorker iterates over to
// populate the canonical localised library (series_texts /
// episode_texts / genres_i18n / keywords_i18n). Order matters for the
// enrichment-pass logging only — the first language is logged first.
// Order is stable per release so log-based smoke tests can assert on
// the first language without churn.
var SupportedUserLanguages = []string{"en-US", "ru-RU"}

// Default returns the canonical fallback tag. Kept as a function so a
// future config-driven seam (env override, etc.) does not require a
// breaking variable rename at call sites.
func Default() string {
	return "en-US"
}
