// Package domain holds the pure, IO-free value objects and aggregate of the
// universal MediaDetail engine (ADR-0022). It imports ONLY shared cross-context
// VOs (internal/shared/domain) — never another bounded context's application or
// infrastructure. The engine is media-type-agnostic: series and movie verticals
// plug in as SectionPlugin adapters (internal/mediadetail/app), the type is a
// parameter (MediaType), never an `if type` branch.
package domain

import (
	"fmt"
	"strings"
)

// MediaType is the value-object discriminator of the engine: which vertical a
// MediaID belongs to. It is a comparable struct (usable as a map key in the
// SectionRegistry) whose only field is unexported, so the sole valid non-zero
// instances are the exported constants MediaTypeSeries / MediaTypeMovie (or a
// value returned by ParseMediaType). The zero value is invalid.
type MediaType struct {
	value string
}

// Unexported backing literals — the canonical wire/DB spellings.
const (
	mediaTypeSeries = "series"
	mediaTypeMovie  = "movie"
)

// Exported constant instances. Structs cannot be Go consts; these are the
// immutable canonical values (never reassigned).
var (
	MediaTypeSeries = MediaType{value: mediaTypeSeries}
	MediaTypeMovie  = MediaType{value: mediaTypeMovie}
)

// ParseMediaType normalizes (trim + lowercase) and validates s. Unknown input
// → ErrInvalidMediaType. Accepts "series" / "movie" case-insensitively.
func ParseMediaType(s string) (MediaType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case mediaTypeSeries:
		return MediaTypeSeries, nil
	case mediaTypeMovie:
		return MediaTypeMovie, nil
	default:
		return MediaType{}, fmt.Errorf("%w: got %q", ErrInvalidMediaType, s)
	}
}

// String returns the canonical spelling ("series"/"movie"), or "" for the zero
// value.
func (t MediaType) String() string { return t.value }

// IsZero reports the invalid zero value.
func (t MediaType) IsZero() bool { return t.value == "" }

// Valid reports whether t is one of the known media types.
func (t MediaType) Valid() bool {
	return t.value == mediaTypeSeries || t.value == mediaTypeMovie
}
