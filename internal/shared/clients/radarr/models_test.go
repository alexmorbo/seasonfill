package radarr

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMovieDTOToPort_QualityAndCodecs covers the movieFile capture path: the
// inline movieFile object on GET /api/v3/movie rows carries the downloaded
// release's quality/resolution and (when Radarr probed the file) its codecs.
func TestMovieDTOToPort_QualityAndCodecs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		raw            string
		wantQuality    *string
		wantResolution *int
		wantVideoCodec *string
		wantAudioCodec *string
	}{
		{
			name: "movieFile with quality and mediaInfo",
			raw: `{"id":7,"title":"Dune","hasFile":true,"movieFile":{
				"quality":{"quality":{"id":7,"name":"Bluray-1080p","resolution":1080}},
				"mediaInfo":{"videoCodec":"x265","audioCodec":"EAC3"}}}`,
			wantQuality:    new("Bluray-1080p"),
			wantResolution: new(1080),
			wantVideoCodec: new("x265"),
			wantAudioCodec: new("EAC3"),
		},
		{
			name: "no movieFile - all nil",
			raw:  `{"id":8,"title":"Arrival","hasFile":false}`,
		},
		{
			name: "movieFile without mediaInfo - codecs nil, quality kept",
			raw: `{"id":9,"title":"Sicario","hasFile":true,"movieFile":{
				"quality":{"quality":{"id":3,"name":"WEBDL-2160p","resolution":2160}}}}`,
			wantQuality:    new("WEBDL-2160p"),
			wantResolution: new(2160),
		},
		{
			name: "hasFile false but movieFile present - not captured",
			raw: `{"id":10,"title":"Stale","hasFile":false,"movieFile":{
				"quality":{"quality":{"id":7,"name":"Bluray-1080p","resolution":1080}}}}`,
		},
		{
			name: "empty quality name and zero resolution stay nil",
			raw: `{"id":11,"title":"Unknown","hasFile":true,"movieFile":{
				"quality":{"quality":{"id":0,"name":"","resolution":0}},
				"mediaInfo":{"videoCodec":"","audioCodec":""}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var d movieDTO
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &d))
			got := movieDTOToPort(d)

			assertPtrEq(t, tt.wantQuality, got.Quality, "quality")
			assertPtrEq(t, tt.wantResolution, got.Resolution, "resolution")
			assertPtrEq(t, tt.wantVideoCodec, got.VideoCodec, "video_codec")
			assertPtrEq(t, tt.wantAudioCodec, got.AudioCodec, "audio_codec")
		})
	}
}

func assertPtrEq[T comparable](t *testing.T, want, got *T, field string) {
	t.Helper()
	if want == nil {
		assert.Nilf(t, got, "%s must be nil", field)
		return
	}
	require.NotNilf(t, got, "%s must be non-nil", field)
	assert.Equalf(t, *want, *got, "%s value", field)
}
