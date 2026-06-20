package omdb

import (
	"strconv"
	"strings"
)

// Enrichment is the typed write payload the OMDb worker hands to
// the series repository. Pointer fields express "this column should
// be NULL on the row" — distinct from "" / 0. The mapper produces
// nil for any "N/A" upstream string per PRD §5.4.
//
// The OMDb worker is the ONLY writer of these four fields per the
// merge policy. Sonarr and TMDB never touch them — the worker
// directly upserts only these columns onto the series row.
type Enrichment struct {
	IMDBRating *float64 // upstream "imdbRating" (decimal string)
	IMDBVotes  *int64   // upstream "imdbVotes"  (comma-formatted string)
	OMDbRated  *string  // upstream "Rated"
	OMDbAwards *string  // upstream "Awards"
}

// Map converts an OMDb Response into the four-field Enrichment.
// Returns the zero-value Enrichment (all-nil fields) for a nil
// response — the worker's contract treats this as "OMDb has nothing
// to write" rather than an error.
//
// Normalisation rules (PRD §5.4):
//  1. Upstream string `"N/A"` (any case) collapses to nil pointer.
//  2. `imdbRating` parses as decimal; parse failure → nil (NULL).
//  3. `imdbVotes` strips ASCII commas before integer parse;
//     parse failure → nil (NULL).
//  4. Empty / whitespace-only string after trim → nil.
func Map(resp *Response) Enrichment {
	if resp == nil {
		return Enrichment{}
	}
	out := Enrichment{}
	if v := parseFloat(resp.IMDBRating); v != nil {
		out.IMDBRating = v
	}
	if v := parseVotes(resp.IMDBVotes); v != nil {
		out.IMDBVotes = v
	}
	if s := normaliseString(resp.Rated); s != nil {
		out.OMDbRated = s
	}
	if s := normaliseString(resp.Awards); s != nil {
		out.OMDbAwards = s
	}
	return out
}

// normaliseString returns nil for empty / whitespace / "N/A"
// upstream strings; otherwise a pointer to the trimmed value.
func normaliseString(raw string) *string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if strings.EqualFold(s, "N/A") {
		return nil
	}
	return &s
}

// parseFloat parses the OMDb rating string into *float64. Empty,
// "N/A", or unparseable returns nil. We use ParseFloat directly —
// OMDb always emits ASCII decimals ("9.5"), no locale-specific
// formatting.
func parseFloat(raw string) *float64 {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "N/A") {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// parseVotes strips ASCII commas from the upstream votes string then
// integer-parses. OMDb formats votes en-US-style ("2,034,123"); we
// remove the grouping commas before ParseInt. Empty, "N/A", or
// unparseable returns nil.
func parseVotes(raw string) *int64 {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "N/A") {
		return nil
	}
	stripped := strings.ReplaceAll(s, ",", "")
	v, err := strconv.ParseInt(stripped, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}
