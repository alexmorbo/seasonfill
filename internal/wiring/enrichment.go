package wiring

// enrichment.go owns the wiring for the enrichment bounded context:
// the external-services runtime-config stack (Story 202 S-2), the
// TMDB/OMDb dispatcher pipeline (Stories 211/212/213), the people
// enrichment workers, the cold-start backfill loop (Story 318), and
// the repo→port adapter shims the application layer reads through.
