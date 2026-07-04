package persistence

// dedupInt64Preserve returns ids with duplicates removed, keeping the
// first occurrence of each value and its relative order. Used by the
// series join Set methods so TMDB-supplied duplicate ids don't violate
// the join's unique (series_id, *_id) constraint.
func dedupInt64Preserve(ids []int64) []int64 {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
