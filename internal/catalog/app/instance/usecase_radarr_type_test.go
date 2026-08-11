package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreate_RadarrType_PersistsAndPublishes proves a type='radarr' instance is
// accepted, persisted with Type carried through, and published on the bus (Ф6-R-6a).
func TestCreate_RadarrType_PersistsAndPublishes(t *testing.T) {
	t.Parallel()
	uc, repo, _, ch := setup(t)
	snap := validSnap("movies")
	snap.Type = "radarr"
	require.NoError(t, uc.Create(context.Background(), snap))

	assert.Equal(t, "radarr", repo.rows["movies"].Type, "Type must persist through Create")

	select {
	case published := <-ch:
		require.Len(t, published.Instances, 1)
		assert.Equal(t, "radarr", published.Instances[0].Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected snapshot on bus within 100ms")
	}
}

// TestCreate_DefaultType_IsSonarr proves an omitted type defaults to "sonarr".
func TestCreate_DefaultType_IsSonarr(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	assert.Equal(t, "sonarr", repo.rows["alpha"].Type)
}

// TestCreate_InvalidType_Rejected proves an unknown discriminator is rejected
// with the typed INVALID_INSTANCE_TYPE validation error.
func TestCreate_InvalidType_Rejected(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	snap := validSnap("alpha")
	snap.Type = "plex"
	err := uc.Create(context.Background(), snap)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
	var verr *ValidationError
	require.True(t, errors.As(err, &verr))
	assert.Equal(t, "INVALID_INSTANCE_TYPE", verr.Code)
}

// TestUpdate_TypeImmutable_IgnoresWireType proves a wire-supplied type on Update
// is ignored — the existing discriminator is preserved (no sonarr↔radarr flip).
func TestUpdate_TypeImmutable_IgnoresWireType(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	create := validSnap("movies")
	create.Type = "radarr"
	require.NoError(t, uc.Create(context.Background(), create))

	upd := validSnap("movies")
	upd.Type = "sonarr" // attempt to flip
	require.NoError(t, uc.Update(context.Background(), "movies", upd, nil))

	assert.Equal(t, "radarr", repo.rows["movies"].Type, "Update must preserve the existing type")
}
