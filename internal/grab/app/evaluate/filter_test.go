package evaluate

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
)

// TestNewPerInstanceUseCase — smoke test that NewPerInstanceUseCase constructs
// a non-nil UseCase with no sonarr client (the per-instance wiring path).
func TestNewPerInstanceUseCase(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := NewPerInstanceUseCase(nil, lg)
	assert.NotNil(t, uc)
}

// stdProfile — IDs 1..3, increasing Order. ID 99 = not-in-profile.
var stdProfile = ports.QualityProfile{
	ID: 100, Name: "test",
	Items: []ports.QualityItem{
		{ID: 1, Name: "SDTV", Order: 1},
		{ID: 2, Name: "HDTV-720p", Order: 2},
		{ID: 3, Name: "WEBDL-1080p", Order: 3},
	},
}

var filterTestNow = time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

func TestFilter(t *testing.T) {
	t.Parallel()
	type want struct {
		keptGUIDs       []string
		filteredCount   int
		filteredReason  string // first FilteredOut[0].Reason
		filteredReasonC string // optional substring assertion (Contains)
	}
	cases := []struct {
		name string
		in   FilterInput
		want want
	}{
		{
			name: "kept: high-quality release survives all checks",
			in: FilterInput{
				Releases: []release.Release{{GUID: "good", QualityID: 3, CustomFormatScore: 100,
					MappedEpisodeNumbers: []int{1, 2, 3}}},
				Missing: []int{1, 2, 3}, Profile: stdProfile, NowUTC: filterTestNow,
			},
			want: want{keptGUIDs: []string{"good"}, filteredCount: 0},
		},
		{
			name: "reject: GUID cooldown drops excluded release",
			in: FilterInput{
				Releases: []release.Release{
					{GUID: "blocked", QualityID: 3, MappedEpisodeNumbers: []int{1}},
					{GUID: "ok", QualityID: 3, MappedEpisodeNumbers: []int{1}},
				},
				Missing: []int{1}, Profile: stdProfile,
				ExcludeGUIDs: map[string]struct{}{"blocked": {}},
			},
			want: want{keptGUIDs: []string{"ok"}, filteredCount: 1,
				filteredReason: string(decision.ReasonFilterGUIDCooldown)},
		},
		{
			name: "reject: Unknown Series",
			in: FilterInput{
				Releases: []release.Release{{GUID: "x", QualityID: 3,
					MappedEpisodeNumbers: []int{1}, Rejections: []string{"Unknown Series"}}},
				Missing: []int{1}, Profile: stdProfile,
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterUnknownSeries)},
		},
		{
			name: "reject: zero coverage (no overlap with missing)",
			in: FilterInput{
				Releases: []release.Release{{GUID: "noop", QualityID: 3, MappedEpisodeNumbers: []int{4, 5}}},
				Missing:  []int{1, 2}, Profile: stdProfile,
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterCoversNothing)},
		},
		{
			name: "reject: quality not in profile",
			in: FilterInput{
				Releases: []release.Release{{GUID: "bluray", QualityID: 99, MappedEpisodeNumbers: []int{1}}},
				Missing:  []int{1}, Profile: stdProfile,
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterQualityNotInProfile)},
		},
		{
			name: "reject: quality downgrade vs existing",
			in: FilterInput{
				Releases: []release.Release{{GUID: "sd", QualityID: 1, MappedEpisodeNumbers: []int{1}}},
				Missing:  []int{1},
				Have:     []series.Episode{{Number: 1, QualityID: 3}},
				Profile:  stdProfile,
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterQualityDowngrade)},
		},
		{
			name: "reject: unsafe rejection reason",
			in: FilterInput{
				Releases: []release.Release{{GUID: "x", QualityID: 3,
					MappedEpisodeNumbers: []int{1}, Rejections: []string{"Manual import required"}}},
				Missing: []int{1}, Profile: stdProfile,
			},
			want: want{filteredCount: 1, filteredReasonC: string(decision.ReasonFilterRejectionsUnsafe)},
		},
		{
			name: "kept: safe-rejection prefix (Full season pack)",
			in: FilterInput{
				Releases: []release.Release{{GUID: "pack", QualityID: 3,
					MappedEpisodeNumbers: []int{1}, Rejections: []string{"Full season pack — superior to current"}}},
				Missing: []int{1}, Profile: stdProfile,
			},
			want: want{keptGUIDs: []string{"pack"}},
		},
		{
			name: "reject: CF score below minimum",
			in: FilterInput{
				Releases: []release.Release{{GUID: "x", QualityID: 3,
					MappedEpisodeNumbers: []int{1}, CustomFormatScore: 5}},
				Missing: []int{1}, Profile: stdProfile, MinCustomFormatScore: 50,
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterCFScoreBelowMin)},
		},
		{
			name: "reject: RequireAllAired with future-airdate episode",
			in: FilterInput{
				Releases: []release.Release{{GUID: "x", QualityID: 3, MappedEpisodeNumbers: []int{1, 2}}},
				Missing:  []int{1, 2},
				Episodes: []series.Episode{
					{Number: 1, AirDateUTC: filterTestNow.Add(-7 * 24 * time.Hour)},
					{Number: 2, AirDateUTC: filterTestNow.Add(24 * time.Hour)},
				},
				Profile: stdProfile, RequireAllAired: true, NowUTC: filterTestNow,
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterAirDateNotReady)},
		},
		{
			name: "kept: RequireAllAired with all episodes already aired",
			in: FilterInput{
				Releases: []release.Release{{GUID: "x", QualityID: 3, MappedEpisodeNumbers: []int{1, 2}}},
				Missing:  []int{1, 2},
				Episodes: []series.Episode{
					{Number: 1, AirDateUTC: filterTestNow.Add(-14 * 24 * time.Hour)},
					{Number: 2, AirDateUTC: filterTestNow.Add(-7 * 24 * time.Hour)},
				},
				Profile: stdProfile, RequireAllAired: true, NowUTC: filterTestNow,
			},
			want: want{keptGUIDs: []string{"x"}},
		},
		{
			name: "ordering: GUID cooldown check beats Unknown Series check",
			in: FilterInput{
				Releases: []release.Release{{GUID: "g", QualityID: 3,
					MappedEpisodeNumbers: []int{1}, Rejections: []string{"Unknown Series"}}},
				Missing: []int{1}, Profile: stdProfile,
				ExcludeGUIDs: map[string]struct{}{"g": {}},
			},
			want: want{filteredCount: 1, filteredReason: string(decision.ReasonFilterGUIDCooldown)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := Filter(tc.in)
			gotKept := make([]string, len(out.Kept))
			for i, r := range out.Kept {
				gotKept[i] = r.GUID
			}
			assert.Equal(t, tc.want.keptGUIDs, nilIfEmpty(gotKept), "Kept GUIDs")
			if tc.want.filteredCount == 0 {
				assert.Empty(t, out.FilteredOut)
				return
			}
			require.Len(t, out.FilteredOut, tc.want.filteredCount)
			if tc.want.filteredReason != "" {
				assert.Equal(t, tc.want.filteredReason, out.FilteredOut[0].Reason)
			}
			if tc.want.filteredReasonC != "" {
				assert.Contains(t, out.FilteredOut[0].Reason, tc.want.filteredReasonC)
			}
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestWouldDowngrade_DirectBranches exercises the branches that Filter's
// short-circuit ordering makes unreachable via Filter alone.
func TestWouldDowngrade_DirectBranches(t *testing.T) {
	t.Parallel()
	t.Run("rel not in profile returns false", func(t *testing.T) {
		t.Parallel()
		rel := release.Release{QualityID: 99}
		assert.False(t, wouldDowngrade(stdProfile, rel, []series.Episode{{Number: 1, QualityID: 3}}))
	})
	t.Run("episode quality not in profile is skipped", func(t *testing.T) {
		t.Parallel()
		rel := release.Release{QualityID: 2}
		// ep with QualityID 99 (not in profile) → skipped; no downgrade
		assert.False(t, wouldDowngrade(stdProfile, rel, []series.Episode{{Number: 1, QualityID: 99}}))
	})
}

// TestRank_OriginIndexerNameBonus exercises the second isOrigin branch.
func TestRank_OriginIndexerNameBonus(t *testing.T) {
	t.Parallel()
	rels := []release.Release{
		{GUID: "a", IndexerName: "NZBGeek", QualityID: 2, MappedEpisodeNumbers: []int{1}},
		{GUID: "b", IndexerName: "Other", QualityID: 2, MappedEpisodeNumbers: []int{1}},
	}
	out := Rank(RankInput{
		Releases:          rels,
		Missing:           []int{1},
		OriginIndexerName: "NZBGeek",
		OriginBonus:       10,
	})
	require.Len(t, out, 2)
	assert.True(t, out[0].IsOriginRelease, "origin-indexer release must be ranked first")
	assert.Equal(t, "a", out[0].Release.GUID)
}

// TestHasUnairedMappedEpisode_DirectBranches — item #6. Exercises the
// 0%-covered helper directly, independent of Filter's short-circuits.
func TestHasUnairedMappedEpisode_DirectBranches(t *testing.T) {
	t.Parallel()
	now := filterTestNow
	cases := []struct {
		name string
		r    release.Release
		eps  []series.Episode
		want bool
	}{
		{"no mapped episodes", release.Release{}, []series.Episode{{Number: 1}}, false},
		{"no episodes", release.Release{MappedEpisodeNumbers: []int{1}}, nil, false},
		{"mapped missing from map",
			release.Release{MappedEpisodeNumbers: []int{99}},
			[]series.Episode{{Number: 1, AirDateUTC: now.Add(time.Hour)}}, false},
		{"zero airdate is unknown",
			release.Release{MappedEpisodeNumbers: []int{1}},
			[]series.Episode{{Number: 1, AirDateUTC: time.Time{}}}, false},
		{"past airdate",
			release.Release{MappedEpisodeNumbers: []int{1}},
			[]series.Episode{{Number: 1, AirDateUTC: now.Add(-24 * time.Hour)}}, false},
		{"future airdate triggers",
			release.Release{MappedEpisodeNumbers: []int{1, 2}},
			[]series.Episode{
				{Number: 1, AirDateUTC: now.Add(-24 * time.Hour)},
				{Number: 2, AirDateUTC: now.Add(48 * time.Hour)},
			}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, hasUnairedMappedEpisode(tc.r, tc.eps, now))
		})
	}
}
