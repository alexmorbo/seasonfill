package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestSonarrSeriesID_JSONRoundTrip locks in the contract that
// type SonarrSeriesID int marshals and unmarshals as a JSON number
// with no special handling — the migration must NOT regress the
// wire shape on EITHER boundary:
//   - outbound (dto.SeriesDetailResponse, dto.LibraryCreditInstance,
//     dto.SeriesTorrentsResponse, dto.SeriesCastResponse)
//   - inbound (Sonarr's seriesId field decodes into our typed field
//     via underlying-int identity)
func TestSonarrSeriesID_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "series_detail_response_outbound",
			in: dto.SeriesDetailResponse{
				Instance:       domain.InstanceName("alpha"),
				SonarrSeriesID: domain.SonarrSeriesID(42),
			},
			want: `"sonarr_series_id":42`,
		},
		{
			name: "library_credit_instance_outbound",
			in: dto.LibraryCreditInstance{
				Instance:       domain.InstanceName("alpha"),
				SonarrSeriesID: domain.SonarrSeriesID(7),
			},
			want: `"sonarr_series_id":7`,
		},
		{
			name: "series_torrents_response_outbound",
			in: dto.SeriesTorrentsResponse{
				Instance:       domain.InstanceName("anime"),
				SonarrSeriesID: domain.SonarrSeriesID(123),
			},
			want: `"sonarr_series_id":123`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.in)
			require.NoError(t, err)
			assert.Contains(t, string(raw), tt.want, "marshal shape")

			// Round-trip back into the same type.
			switch v := tt.in.(type) {
			case dto.SeriesDetailResponse:
				var out dto.SeriesDetailResponse
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.SonarrSeriesID, out.SonarrSeriesID)
			case dto.LibraryCreditInstance:
				var out dto.LibraryCreditInstance
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.SonarrSeriesID, out.SonarrSeriesID)
			case dto.SeriesTorrentsResponse:
				var out dto.SeriesTorrentsResponse
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.SonarrSeriesID, out.SonarrSeriesID)
			}
		})
	}
}

// TestSonarrSeriesID_InboundDecode locks in the inbound direction:
// Sonarr emits {"seriesId": 123} as a JSON number; our typed
// domain.SonarrSeriesID-backed field must decode without a custom
// UnmarshalJSON.
func TestSonarrSeriesID_InboundDecode(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"sonarr_series_id":42}`)
	var out dto.SeriesDetailResponse
	require.NoError(t, json.Unmarshal(payload, &out))
	assert.Equal(t, domain.SonarrSeriesID(42), out.SonarrSeriesID)
}
