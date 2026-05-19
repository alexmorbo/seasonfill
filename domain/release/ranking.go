package release

type Scored struct {
	Release          Release
	Coverage         int
	IsOriginRelease  bool
	OriginBonusValue float64
}

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
