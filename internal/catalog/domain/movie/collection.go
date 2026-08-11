package movie

// CollectionCanon is the persistence-neutral TMDB-collection row (Ф6-R-5). It
// maps to the `collections` table created by migration 000053. Enrichment writes
// ONLY the TMDB-owned fields (name/overview/*_asset via the COALESCE upsert);
// Monitored + RadarrMonitored are OPERATOR/Radarr-owned flags that the
// enrichment path never blanks — they are populated on read but excluded from
// UpsertCollection's assignment set. TMDBCollectionID is the raw TMDB id that
// movies.collection_id points at (the franchise-membership link).
type CollectionCanon struct {
	TMDBCollectionID int
	Name             string
	Overview         *string
	PosterAsset      *string
	BackdropAsset    *string
	// Monitored / RadarrMonitored are read-populated only; MapCollectionToCanon
	// leaves them zero and UpsertCollection never writes them.
	Monitored       bool
	RadarrMonitored bool
}
