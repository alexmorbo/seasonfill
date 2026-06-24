package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestDedupItemsBySeriesID_PreservesFirstOccurrence(t *testing.T) {
	items := []disco.Item{
		{SeriesID: 100, Title: "first"},
		{SeriesID: 200, Title: "second"},
		{SeriesID: 100, Title: "dup-of-first"},
		{SeriesID: 300, Title: "third"},
		{SeriesID: 200, Title: "dup-of-second"},
	}
	out := dedupItemsBySeriesID(items)
	require.Len(t, out, 3)
	require.Equal(t, shareddomain.SeriesID(100), out[0].SeriesID)
	require.Equal(t, "first", out[0].Title)
	require.Equal(t, shareddomain.SeriesID(200), out[1].SeriesID)
	require.Equal(t, "second", out[1].Title)
	require.Equal(t, shareddomain.SeriesID(300), out[2].SeriesID)
}
