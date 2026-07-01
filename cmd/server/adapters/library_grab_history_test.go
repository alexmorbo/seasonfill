package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	grabdomain "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeGrabLister struct {
	recs      []grabdomain.Record
	err       error
	gotFilter ports.GrabFilter
	gotPage   ports.Pagination
}

func (f *fakeGrabLister) List(_ context.Context, filter ports.GrabFilter, page ports.Pagination) ([]grabdomain.Record, *ports.Cursor, error) {
	f.gotFilter = filter
	f.gotPage = page
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.recs, nil, nil
}

func TestGrabHistoryAdapter_RecentBySeries_MapsAndFilters(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fake := &fakeGrabLister{recs: []grabdomain.Record{
		{Status: grabdomain.StatusImported, SeasonNumber: 3, ReleaseTitle: "Show.S03E01", Quality: "WEB-DL 1080p", CreatedAt: now, UpdatedAt: now.Add(5 * time.Minute)},
		{Status: grabdomain.StatusGrabbed, SeasonNumber: 3, ReleaseTitle: "Show.S03E02", Quality: "WEB-DL 720p", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}}
	a := NewGrabHistoryAdapter(fake)

	out, err := a.RecentBySeries(context.Background(), "homelab", 7, 5)
	require.NoError(t, err)
	require.Len(t, out, 2)

	assert.Equal(t, "imported", out[0].Status)
	assert.Equal(t, 3, out[0].SeasonNumber)
	assert.Equal(t, "Show.S03E01", out[0].ReleaseTitle)
	assert.Equal(t, "WEB-DL 1080p", out[0].Quality)
	assert.Equal(t, now, out[0].CreatedAt)
	assert.Equal(t, now.Add(5*time.Minute), out[0].UpdatedAt)
	assert.Equal(t, "grabbed", out[1].Status)

	require.NotNil(t, fake.gotFilter.Instance)
	assert.Equal(t, domain.InstanceName("homelab"), *fake.gotFilter.Instance)
	require.NotNil(t, fake.gotFilter.SeriesID)
	assert.Equal(t, domain.SonarrSeriesID(7), *fake.gotFilter.SeriesID)
	assert.Equal(t, 5, fake.gotPage.Limit)
}

func TestGrabHistoryAdapter_RecentBySeries_DefaultLimit(t *testing.T) {
	t.Parallel()
	fake := &fakeGrabLister{}
	a := NewGrabHistoryAdapter(fake)

	out, err := a.RecentBySeries(context.Background(), "homelab", 7, 0)
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Equal(t, 5, fake.gotPage.Limit, "non-positive limit clamps to 5")
}

func TestGrabHistoryAdapter_RecentBySeries_Error(t *testing.T) {
	t.Parallel()
	fake := &fakeGrabLister{err: errors.New("db down")} //nolint:err113
	a := NewGrabHistoryAdapter(fake)

	_, err := a.RecentBySeries(context.Background(), "homelab", 7, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grab history recent by series")
}
