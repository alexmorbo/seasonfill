package rest

import (
	domenrich "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// sourceStringSlice projects []enrichment.Source → []string for the
// wire. Same shape as the legacy handler-package helper of the same
// name (see interface/http/handlers/series_detail.go); duplicated here
// because the enrichment REST handlers were extracted into their own
// bounded-context package by story 438 A-1-12 and the helper has no
// natural shared home yet (story 449 will revisit the ports/util
// split).
func sourceStringSlice(s []domenrich.Source) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		out = append(out, string(v))
	}
	return out
}
