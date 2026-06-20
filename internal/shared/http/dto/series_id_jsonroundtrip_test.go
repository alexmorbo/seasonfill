package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// TestSeriesID_JSONRoundTrip locks in the contract that
// type SeriesID int64 marshals and unmarshals as a JSON number with no
// special handling — the story 399 migration must NOT regress the wire
// shape. Three DTOs cover the SeriesID wire surface: SeriesDetailResponse,
// SeriesRefreshResponse, LibraryCreditEntry (person.go).
func TestSeriesID_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want string // expected JSON fragment ("series_id":<n>)
	}{
		{
			name: "series_detail_response",
			in: dto.SeriesDetailResponse{
				Instance: "alpha",
				SeriesID: domain.SeriesID(42),
			},
			want: `"series_id":42`,
		},
		{
			name: "series_refresh_response",
			in: dto.SeriesRefreshResponse{
				SeriesID:     domain.SeriesID(123456789012345),
				SeriesQueued: true,
			},
			want: `"series_id":123456789012345`,
		},
		{
			name: "library_credit_entry",
			in:   dto.LibraryCreditEntry{SeriesID: domain.SeriesID(7)},
			want: `"series_id":7`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.in)
			require.NoError(t, err)
			assert.Contains(t, string(raw), tt.want, "marshal shape")

			switch v := tt.in.(type) {
			case dto.SeriesDetailResponse:
				var out dto.SeriesDetailResponse
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.SeriesID, out.SeriesID)
			case dto.SeriesRefreshResponse:
				var out dto.SeriesRefreshResponse
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.SeriesID, out.SeriesID)
			case dto.LibraryCreditEntry:
				var out dto.LibraryCreditEntry
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.SeriesID, out.SeriesID)
			}
		})
	}
}
