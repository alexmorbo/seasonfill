package release

type Scored struct {
	Release          Release
	Coverage         int
	IsOriginRelease  bool
	OriginBonusValue float64
}

// Less orders candidates by CFS first to mirror Sonarr's own rejection signal
// ("Existing file on disk has a equal or higher CF score"). In practice all
// viable candidates share the same CFS, so the real tie-breaker becomes
// Coverage — which matches the design intent of preferring the release that
// fills the most missing episodes. Origin stickiness, indexer priority,
// seeders and size then break further ties.
func (s Scored) Less(other Scored) bool {
	if s.Release.CustomFormatScore != other.Release.CustomFormatScore {
		return s.Release.CustomFormatScore > other.Release.CustomFormatScore
	}
	if s.Coverage != other.Coverage {
		return s.Coverage > other.Coverage
	}
	if s.IsOriginRelease != other.IsOriginRelease {
		return s.IsOriginRelease
	}
	if s.Release.IndexerPriority != other.Release.IndexerPriority {
		return s.Release.IndexerPriority < other.Release.IndexerPriority
	}
	if s.Release.Seeders != other.Release.Seeders {
		return s.Release.Seeders > other.Release.Seeders
	}
	return s.Release.SizeBytes > other.Release.SizeBytes
}
