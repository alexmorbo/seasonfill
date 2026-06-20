// Package grab hosts the bounded context's application service —
// UseCase, which drives a single Sonarr grab attempt through M-7's
// three-write atomic success path (grab_records.Create +
// cooldowns.Set + origin_releases.Upsert) wrapped by an optional
// Transactor, with backoff retries on transient failure. The
// directory name is `app` (PRD §3.2 layout) but the Go package keeps
// its established short name `grab` so consumers' import paths churn
// only on the trailing path segment without an alias rename. A later
// normalization story can rename `grab` → `app` everywhere once the
// dust on Phase 1 has settled (mirrors the admin/app `auth` carve-out
// from story 428).
//
// ports.go gathers the narrow port interfaces and exported entry-
// points this layer publishes to its consumers. The interfaces
// themselves are declared in their owning files (so the production
// impl + test fakes live next to the interface that constrains them)
// — this file is an index pointing at each port so future readers can
// find the contract surface without grepping. Story 431 (A-1-5)
// carved out the bounded context; story 449 (model split) +
// follow-ups will relocate the operator-visible GrabRepository
// interface decl OUT of application/ports and into this file. Until
// then, see:
//
//   - UseCase (grab_usecase.go) — Execute one grab decision. Holds
//     ports.GrabRepository (writes), ports.CooldownRepository (M-7
//     transactional cooldown set), ports.OriginReleaseRepository
//     (M-7 transactional origin upsert), a Sonarr ports.Classifier
//     (status taxonomy), and an optional ports.Transactor (success-
//     path atomic wrapper — nil = direct writes). WithSleeper +
//     WithTransactor wire collaborators post-construction.
//
//   - backoffFor (backoff.go) — pure-fn backoff schedule (1s, 5s,
//     30s, 2m, 10m), capped at 6 attempts. Returns the sleep
//     duration before attempt N (1-indexed).
//
// Cross-package consumers (interface/http/handlers + internal/grab/
// rest + application/regrab + application/scan + cmd/server/wiring)
// import these names directly from package grab via the import path
// `github.com/alexmorbo/seasonfill/internal/grab/app` — the bare
// package identifier `grab` survived the story 431 move unchanged.

package grab
