package freshener

import (
	"fmt"
	"strconv"
	"strings"
)

// Section identifies a freshness scope. Fixed sections are constant
// enum values; dynamic season sections use the prefix "season:" +
// decimal season number (e.g. "season:8").
type Section string

const (
	SectionSkeleton        Section = "skeleton"        // hero + season summaries (per PLAN §7.1 B1a)
	SectionOverview        Section = "overview"        // overview block (series_texts.lang)
	SectionCast            Section = "cast"            // series_people + characters_i18n[lang]
	SectionRecommendations Section = "recommendations" // series_recommendations + N×UPSERT series_texts side-effect (PLAN §6.3.5)
	SectionMedia           Section = "media"           // poster/backdrop/logo/asset hashes
)

// FixedSections is the canonical DENSE iteration order. DBProbe always
// emits one verdict per element here, in this order (predictable
// downstream iteration in EnsureFreshScope, ChangesSyncer dispatch).
var FixedSections = []Section{
	SectionSkeleton,
	SectionOverview,
	SectionCast,
	SectionRecommendations,
	SectionMedia,
}

// seasonPrefix is the dynamic-section marker. Kept lowercase to match
// the enum naming convention; the integer is decimal with no padding.
const seasonPrefix = "season:"

// SeasonSection builds the Section value for a specific season number.
// Negative numbers are caller error (Sonarr season numbering allows
// 0 = specials, so we reject only < 0).
func SeasonSection(n int) Section {
	if n < 0 {
		return Section(fmt.Sprintf("%s%d", seasonPrefix, n)) // still typed, but caller bug
	}
	return Section(seasonPrefix + strconv.Itoa(n))
}

// IsSeasonSection returns (N, true) if s has the season:N shape, else
// (0, false). Caller uses this to dispatch SeasonSection verdicts to
// A3a RefreshSeasonSlim.
func IsSeasonSection(s Section) (int, bool) {
	str := string(s)
	if !strings.HasPrefix(str, seasonPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(str, seasonPrefix))
	if err != nil {
		return 0, false
	}
	return n, true
}
