package radarr

import (
	"context"
	"net/url"
	"strconv"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// collections.go — Ф6-R-5 Radarr v3 native-collection endpoints. These methods
// live on the concrete *radarr.Client and are DELIBERATELY NOT part of the
// ports.RadarrClient interface (no moq regen); the R-5 collection-monitor usecase
// consumes them via a narrow RadarrCollectionClient port. DORMANT until R-6 wires
// a Radarr instance to the usecase.

type collectionDTO struct {
	ID                  int    `json:"id"`
	Title               string `json:"title"`
	TMDBID              int    `json:"tmdbId"`
	Monitored           bool   `json:"monitored"`
	SearchOnAdd         bool   `json:"searchOnAdd"`
	QualityProfileID    int    `json:"qualityProfileId"`
	MinimumAvailability string `json:"minimumAvailability"`
	RootFolderPath      string `json:"rootFolderPath"`
}

func collectionDTOToPort(d collectionDTO) ports.RadarrCollection {
	return ports.RadarrCollection{
		ID: d.ID, Title: d.Title, TMDBID: d.TMDBID, Monitored: d.Monitored,
		SearchOnAdd: d.SearchOnAdd, QualityProfileID: d.QualityProfileID,
		MinimumAvailability: d.MinimumAvailability, RootFolderPath: d.RootFolderPath,
	}
}

func collectionPortToDTO(c ports.RadarrCollection) collectionDTO {
	return collectionDTO{
		ID: c.ID, Title: c.Title, TMDBID: c.TMDBID, Monitored: c.Monitored,
		SearchOnAdd: c.SearchOnAdd, QualityProfileID: c.QualityProfileID,
		MinimumAvailability: c.MinimumAvailability, RootFolderPath: c.RootFolderPath,
	}
}

// GetCollections calls GET /api/v3/collection — the full collection list. Used to
// resolve our tmdb_collection_id to Radarr's own numeric collection id (they
// differ) before PutCollection.
func (c *Client) GetCollections(ctx context.Context) ([]ports.RadarrCollection, error) {
	var dtos []collectionDTO
	if err := c.get(ctx, "/api/v3/collection", url.Values{}, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.RadarrCollection, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, collectionDTOToPort(d))
	}
	return out, nil
}

// GetCollection calls GET /api/v3/collection/{id} (Radarr's own id).
func (c *Client) GetCollection(ctx context.Context, id int) (ports.RadarrCollection, error) {
	var d collectionDTO
	if err := c.get(ctx, "/api/v3/collection/"+strconv.Itoa(id), url.Values{}, &d); err != nil {
		return ports.RadarrCollection{}, err
	}
	return collectionDTOToPort(d), nil
}

// PutCollection calls PUT /api/v3/collection/{id} with the full resource so Radarr
// enables (or disables) native collection monitoring (monitored + monitorMovies
// via searchOnAdd). The caller passes the round-tripped resource with Monitored
// flipped. id is c.ID.
func (c *Client) PutCollection(ctx context.Context, col ports.RadarrCollection) error {
	body := collectionPortToDTO(col)
	return c.Put(ctx, "/api/v3/collection/"+strconv.Itoa(col.ID), body, nil)
}
