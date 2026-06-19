package domain_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestEpisodeID_JSONRoundTrip verifies the typed primitive marshals as
// a plain JSON number and survives a round-trip without loss. Story 405
// A-5d-4 — the migration treats episode_id as an int64 with a typed
// alias, so the wire format must remain unchanged (downstream JSON
// consumers see the same numeric shape they always have).
func TestEpisodeID_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	type wrap struct {
		ID domain.EpisodeID `json:"id"`
	}
	in := wrap{ID: domain.EpisodeID(9223372036854775807)} // max int64
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{"id":9223372036854775807}`, string(b))
	var out wrap
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in.ID, out.ID)
}

// TestReservedCanonIDs_Compile is a compile-time check that the typed
// IDs reserved by story 405 A-5d-4 (UserID, InstanceID, GrabID) stay
// declared in ids.go. The constructor expressions fail to compile if
// any type is removed — guarding the operator-chosen Option B
// reservation (kept across all four canon primitives).
func TestReservedCanonIDs_Compile(t *testing.T) {
	t.Parallel()
	_ = domain.UserID(0)
	_ = domain.InstanceID(0)
	_ = domain.GrabID(0)
	_ = domain.EpisodeID(0)
}

// TestTVDBID_JSONRoundTrip — story 404 A-5d-3. TVDBID is an int
// underneath; the JSON wire format must remain a plain number so
// downstream consumers (Sonarr inbound, TMDB external_ids embed,
// /api/v1/series/.../detail outbound) see the identical shape they
// did before the typed migration.
func TestTVDBID_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	type wrap struct {
		ID domain.TVDBID `json:"id"`
	}
	in := wrap{ID: domain.TVDBID(54321)}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{"id":54321}`, string(b))
	var out wrap
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in.ID, out.ID)
}

// TestTVDBID_PointerJSONNull — story 404. The pointer form must
// honour `omitempty` (nil → field absent) and marshal a non-nil
// value as a plain number, matching the Canon / CacheEntry /
// SeriesDetail DTO usage of *domain.TVDBID.
func TestTVDBID_PointerJSONNull(t *testing.T) {
	t.Parallel()
	type wrap struct {
		ID *domain.TVDBID `json:"id,omitempty"`
	}
	in := wrap{}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{}`, string(b))
	id := domain.TVDBID(54321)
	in.ID = &id
	b, err = json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{"id":54321}`, string(b))
}

// TestIMDBID_JSONRoundTrip — story 402 A-5d-1. IMDBID is a string
// underneath; the JSON wire format must remain a plain string so
// downstream consumers (Sonarr inbound, TMDB external_ids embed,
// SeriesDetail / refresh DTOs) see the identical "tt..." shape they
// did before the typed migration.
func TestIMDBID_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	type wrap struct {
		ID domain.IMDBID `json:"id"`
	}
	in := wrap{ID: domain.IMDBID("tt0944947")}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{"id":"tt0944947"}`, string(b))
	var out wrap
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in.ID, out.ID)
}

// TestIMDBID_PointerJSONNull — story 402. The pointer form must
// honour `omitempty` (nil → field absent) and marshal a non-nil
// value as a plain string, matching the Canon / CacheEntry /
// SeriesDetail DTO usage of *domain.IMDBID.
func TestIMDBID_PointerJSONNull(t *testing.T) {
	t.Parallel()
	type wrap struct {
		ID *domain.IMDBID `json:"id,omitempty"`
	}
	in := wrap{}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{}`, string(b))
	id := domain.IMDBID("tt1234567")
	in.ID = &id
	b, err = json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{"id":"tt1234567"}`, string(b))
}

func TestNewIMDBID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    domain.IMDBID
		wantErr bool
	}{
		{name: "valid_short", input: "tt0000001", want: "tt0000001"},
		{name: "valid_long", input: "tt123456789", want: "tt123456789"},
		{name: "valid_leading_whitespace", input: "  tt0944947", want: "tt0944947"},
		{name: "valid_trailing_whitespace", input: "tt0944947\n", want: "tt0944947"},
		{name: "invalid_uppercase_prefix", input: "TT0000001", wantErr: true},
		{name: "invalid_no_digits", input: "tt", wantErr: true},
		{name: "invalid_digits_only", input: "123", wantErr: true},
		{name: "invalid_alpha", input: "abc", wantErr: true},
		{name: "invalid_empty", input: "", wantErr: true},
		{name: "invalid_whitespace_only", input: "   ", wantErr: true},
		{name: "invalid_letter_suffix", input: "tt12a34", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewIMDBID(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidIMDBID),
					"want ErrInvalidIMDBID, got %v", err)
				assert.Equal(t, domain.IMDBID(""), got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestQbitHash_JSONRoundTrip — story 406 A-5d-5. QbitHash is a string
// underneath; the JSON wire format must remain a plain string so
// downstream consumers (grab DTO, torrents tab, audit endpoint) see
// the identical lowercase 40-char hex shape they did before the typed
// migration.
func TestQbitHash_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	type wrap struct {
		H domain.QbitHash `json:"h"`
	}
	in := wrap{H: domain.QbitHash("0123456789abcdef0123456789abcdef01234567")}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{"h":"0123456789abcdef0123456789abcdef01234567"}`, string(b))
	var out wrap
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in.H, out.H)
}

// TestQbitHash_PointerJSONNull — story 406. The pointer form must
// honour `omitempty` (nil → field absent) and marshal a non-nil
// value as a plain string, matching the grab.Record / DTO usage of
// *domain.QbitHash.
func TestQbitHash_PointerJSONNull(t *testing.T) {
	t.Parallel()
	type wrap struct {
		H *domain.QbitHash `json:"h,omitempty"`
	}
	in := wrap{}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{}`, string(b))
	h := domain.QbitHash("aabbccddeeff00112233445566778899aabbccdd")
	in.H = &h
	b, err = json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t,
		`{"h":"aabbccddeeff00112233445566778899aabbccdd"}`, string(b))
}

func TestNewQbitHash(t *testing.T) {
	t.Parallel()

	const valid40 = "0123456789abcdef0123456789abcdef01234567"
	const valid40Upper = "0123456789ABCDEF0123456789ABCDEF01234567"

	tests := []struct {
		name    string
		input   string
		want    domain.QbitHash
		wantErr bool
	}{
		{name: "valid_lowercase", input: valid40, want: domain.QbitHash(valid40)},
		{name: "valid_uppercase_normalized", input: valid40Upper, want: domain.QbitHash(valid40)},
		{name: "valid_mixed_case_normalized", input: "0123456789aBcDeF0123456789ABCDEF01234567", want: domain.QbitHash(valid40)},
		{name: "valid_with_leading_whitespace", input: "  " + valid40, want: domain.QbitHash(valid40)},
		{name: "valid_with_trailing_whitespace", input: valid40 + "\t\n", want: domain.QbitHash(valid40)},
		{name: "invalid_39_chars", input: valid40[:39], wantErr: true},
		{name: "invalid_41_chars", input: valid40 + "0", wantErr: true},
		{name: "invalid_non_hex_g", input: "g123456789abcdef0123456789abcdef01234567", wantErr: true},
		{name: "invalid_non_hex_xyz", input: "xyz3456789abcdef0123456789abcdef01234567", wantErr: true},
		{name: "invalid_empty", input: "", wantErr: true},
		{name: "invalid_whitespace_only", input: "    ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewQbitHash(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidQbitHash),
					"want ErrInvalidQbitHash, got %v", err)
				assert.Equal(t, domain.QbitHash(""), got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
