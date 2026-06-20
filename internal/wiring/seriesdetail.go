package wiring

// seriesdetail.go owns the wiring for the seriesdetail bounded
// context: the Story 215 G-1 composer + handlers, the Story 216 H-1
// cast composer, the Story 217 H-2 people UC, and the Story 218 E-2
// series-refresh trigger. The MediaResolver constructed here is
// late-bound from server.go's LATE BIND ZONE (Story 316) and the
// PersonEnqueuerHolder is filled with the dispatcher once enrichment
// boots.
