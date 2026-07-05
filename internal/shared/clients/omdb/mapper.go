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
// The OMDb worker is the ONLY writer of these six fields per the
// merge policy. Sonarr and TMDB never touch them — the worker
// directly upserts only these columns onto the series row.
type Enrichment struct {
	IMDBRating *float64 // upstream "imdbRating" (decimal string)
	IMDBVotes  *int64   // upstream "imdbVotes"  (comma-formatted string)
	OMDbRated  *string  // upstream "Rated"
	OMDbAwards *string  // upstream "Awards"
	// OMDbRTRating / OMDbMetacritic — Story 1039. Parsed out of the
	// upstream `Ratings` array (already present in the same response;
	// previously decoded nowhere). The IMDb entry in that same array
	// is ignored — `imdbRating` above is already the scalar tie-breaker
	// for the header.
	OMDbRTRating   *int // Rotten Tomatoes percent, "91%"    -> 91
	OMDbMetacritic *int // Metacritic score,       "69/100" -> 69
}

// Map converts an OMDb Response into the six-field Enrichment.
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
//  5. `Ratings[].Value` for source "Rotten Tomatoes" strips a
//     trailing '%' before integer parse; for "Metacritic" takes the
//     numerator before '/' before integer parse. Missing source,
//     empty/"N/A" value, or parse failure → nil.
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
	out.OMDbRTRating = parseRTRating(resp.Ratings)
	out.OMDbMetacritic = parseMetacritic(resp.Ratings)
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

// parseRTRating scans the OMDb `Ratings` array for the
// "Rotten Tomatoes" source and parses its percent Value ("91%" ->
// 91). Missing source, empty/"N/A" value, or a value that doesn't
// parse as an integer percent returns nil.
func parseRTRating(ratings []Rating) *int {
	for _, r := range ratings {
		if r.Source != "Rotten Tomatoes" {
			continue
		}
		s := strings.TrimSpace(r.Value)
		if s == "" || strings.EqualFold(s, "N/A") {
			return nil
		}
		s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
		v, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		return &v
	}
	return nil
}

// parseMetacritic scans the OMDb `Ratings` array for the
// "Metacritic" source and parses the numerator of its Value
// ("69/100" -> 69). Missing source, empty/"N/A" value, or an
// unparseable numerator returns nil.
func parseMetacritic(ratings []Rating) *int {
	for _, r := range ratings {
		if r.Source != "Metacritic" {
			continue
		}
		s := strings.TrimSpace(r.Value)
		if s == "" || strings.EqualFold(s, "N/A") {
			return nil
		}
		numerator, _, _ := strings.Cut(s, "/")
		v, err := strconv.Atoi(strings.TrimSpace(numerator))
		if err != nil {
			return nil
		}
		return &v
	}
	return nil
}
