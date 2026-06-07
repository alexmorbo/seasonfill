package series

// SeasonStats is a denormalised per-season counter triplet derived from
// Sonarr's season.statistics block. It exists as a value object (no
// behaviour beyond pure derivations) so the 046b scan pre-filter can be
// unit-tested without constructing a full series.Season tree.
//
// Field semantics:
//
//	Total    = season.statistics.totalEpisodeCount  // all episodes (incl. unaired)
//	Aired    = season.statistics.airedEpisodeCount  // subset that have aired
//	Existing = season.statistics.episodeFileCount   // subset on disk
//
// "Missing" is the partial-pack-aware delta Aired-Existing, clamped to
// non-negative (Sonarr occasionally returns inconsistent mid-import
// snapshots where Existing momentarily exceeds Aired).
type SeasonStats struct {
	Total      int
	Aired      int
	Existing   int
	SizeOnDisk int64
}

// Missing returns aired-but-not-on-disk count, clamped to zero. This is
// the figure the scan pre-filter routes on — "do we still need to look
// for episodes that have actually aired?".
func (s SeasonStats) Missing() int {
	d := s.Aired - s.Existing
	if d < 0 {
		return 0
	}
	return d
}

// IsComplete reports whether every aired episode is on disk. Equivalent
// to Missing()==0. A season with Total=10, Aired=0, Existing=0 (entirely
// unaired) IS complete by this definition — there is nothing seasonfill
// can fetch yet, so it's correctly skipped.
func (s SeasonStats) IsComplete() bool { return s.Missing() <= 0 }

// HasNoLocal reports whether nothing is on disk yet AND at least one
// episode has aired. This is the "Sonarr just hasn't grabbed anything
// yet" case — seasonfill has no upgrade to make on top of nothing,
// so 046b's pre-filter routes these to a Decision with
// Reason=skip_all_episodes_missing (Category=sonarr_handles) without
// burning a Sonarr SearchReleases call.
func (s SeasonStats) HasNoLocal() bool { return s.Existing == 0 && s.Aired > 0 }

// SeasonStatsFromStatistics adapts the existing per-season Statistics
// value into the partial-pack-aware triplet. Keeps the construction in
// one place so call sites stay tidy.
func SeasonStatsFromStatistics(st Statistics) SeasonStats {
	aired := st.Aired
	if aired == 0 {
		// Legacy callsites (pre-046a) only populated EpisodeCount; treat
		// EpisodeCount as aired in that case so the value object stays
		// useful on partially-decoded fixtures.
		aired = st.EpisodeCount
	}
	total := st.Total
	if total == 0 {
		total = aired
	}
	return SeasonStats{Total: total, Aired: aired, Existing: st.EpisodeFileCount}
}
