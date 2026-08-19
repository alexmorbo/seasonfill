package domain

// MediaDetail is the normalized, type-agnostic view-model aggregate the engine
// assembles for one media detail open — the BE mirror of the FE MediaDetailVM.
//
// S1 SCOPE: this is intentionally a near-empty skeleton carrying only the
// identity. Later ADR-0022 section stories grow it field-by-field (text, cast,
// recs, media, keywords, seasons, collection, hero) as each plugin's reader is
// composed in. Keeping it minimal now avoids inventing a schema before the
// section plugins exist.
type MediaDetail struct {
	ID MediaID
}

// Type is a convenience accessor for the media vertical.
func (d MediaDetail) Type() MediaType { return d.ID.Type() }
