package grab

// Parsed is the value object representing one Sonarr-parsed release.
// All fields are optional — a Parsed{} zero value is meaningful and
// emits as `parsed: {}` on the API. Pointer-on-Record handles the
// "absent vs empty" distinction at persistence time.
type Parsed struct {
	Codec        string
	Source       string
	Quality      string
	Resolution   int
	HDRFlags     []string
	Dub          string
	Languages    []string
	Subs         []string
	ReleaseGroup string
}

// IsZero reports whether the value carries no information. A zero
// Parsed differs from absent: the repo persists IsZero() Parsed as an
// empty-but-present row (parsed_at != NULL); absent means the column
// is NULL and the API emits `parsed: null`. The webhook use case in
// 044b uses IsZero to decide whether to emit the noop_metric.
func (p Parsed) IsZero() bool {
	return p.Codec == "" &&
		p.Source == "" &&
		p.Quality == "" &&
		p.Resolution == 0 &&
		len(p.HDRFlags) == 0 &&
		p.Dub == "" &&
		len(p.Languages) == 0 &&
		len(p.Subs) == 0 &&
		p.ReleaseGroup == ""
}
