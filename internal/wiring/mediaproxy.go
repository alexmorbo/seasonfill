package wiring

// mediaproxy.go owns the wiring for the mediaproxy bounded context:
// the Story 214 F-1 media pipeline (mediastore.Store + media_assets
// repo + HTTP MediaHandler). The OnDemandFetcher is late-bound from
// server.go's LATE BIND ZONE (Story 321) — the handler is constructed
// here with PendingResolver but no OnDemandFetcher so the embedded
// SVG placeholder remains the boot fallback.
