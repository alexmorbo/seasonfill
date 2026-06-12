package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
)

func TestExternalIDsRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	repo := NewExternalIDsRepository(db)

	require.NoError(t, repo.Upsert(ctx, enrichment.EntityTypeSeries, seriesID, "wikidata", "Q108042401"))

	got, err := repo.Get(ctx, enrichment.EntityTypeSeries, seriesID, "wikidata")
	require.NoError(t, err)
	assert.Equal(t, "Q108042401", got.Value)
}

func TestExternalIDsRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewExternalIDsRepository(db)
	_, err := repo.Get(context.Background(), enrichment.EntityTypeSeries, 1, "wikidata")
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestExternalIDsRepository_Upsert_InvalidEntityType(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewExternalIDsRepository(db)
	err := repo.Upsert(context.Background(), enrichment.EntityType("invalid"), 1, "wikidata", "x")
	require.Error(t, err)
}

// TestExternalIDsRepository_Polymorphic_AcrossEntityTypes covers the
// acceptance criterion: idempotent upsert across all three entity_types
// (series/person/episode). The composite PK
// (entity_type, entity_id, provider) makes the same entity_id valid
// across distinct entity_type values without conflict.
func TestExternalIDsRepository_Polymorphic_AcrossEntityTypes(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewExternalIDsRepository(db)

	const sameID = int64(42)
	cases := []struct {
		entityType enrichment.EntityType
		provider   string
		value      string
	}{
		{enrichment.EntityTypeSeries, "wikidata", "Q-series-42"},
		{enrichment.EntityTypePerson, "wikidata", "Q-person-42"},
		{enrichment.EntityTypeEpisode, "tvdb", "ep-42"},
	}
	for _, tc := range cases {
		require.NoError(t, repo.Upsert(ctx, tc.entityType, sameID, tc.provider, tc.value))
		// Idempotent re-upsert.
		require.NoError(t, repo.Upsert(ctx, tc.entityType, sameID, tc.provider, tc.value))
	}

	for _, tc := range cases {
		got, err := repo.Get(ctx, tc.entityType, sameID, tc.provider)
		require.NoError(t, err)
		assert.Equal(t, tc.value, got.Value,
			"entity_id %d MUST address distinct rows across entity_type", sameID)
	}
}

func TestExternalIDsRepository_ListByEntity(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	repo := NewExternalIDsRepository(db)

	providers := []string{"facebook", "instagram", "twitter"}
	for _, p := range providers {
		require.NoError(t, repo.Upsert(ctx, enrichment.EntityTypeSeries, seriesID, p, "handle-"+p))
	}

	rows, err := repo.ListByEntity(ctx, enrichment.EntityTypeSeries, seriesID)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	// Ordered by provider ASC.
	assert.Equal(t, "facebook", rows[0].Provider)
	assert.Equal(t, "instagram", rows[1].Provider)
	assert.Equal(t, "twitter", rows[2].Provider)
}
