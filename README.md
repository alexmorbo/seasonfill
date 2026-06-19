<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/logo-dark.png">
    <img src="docs/logo-light.png" alt="seasonfill" width="360">
  </picture>
</p>

# Seasonfill

A companion service for [Sonarr](https://sonarr.tv) that automates
grabbing updated season packs when Sonarr's native upgrade logic
refuses to. Multi-instance, scheduled, with a webhook receiver that
closes the import-success/failure loop and a small React UI for
operator override.

> **Русская версия:** [README.ru.md](README.ru.md).

> **Project status: beta.** Public API stable (config, CLI, REST endpoints, DB
> schema). Image tags pinned in production. Chart version tracks app version
> for compatibility (currently 0.6.2).

## Quickstart

Pick a deploy path:

- **Docker Compose** — single-host, easiest. See
  [`deploy/compose/README.md`](deploy/compose/README.md).
- **Kubernetes via Helm** — production / homelab clusters. Chart at
  `oci://ghcr.io/alexmorbo/seasonfill-helm`. See
  [`deploy/helm/seasonfill/README.md`](deploy/helm/seasonfill/README.md).

Either path brings up two containers (Go backend + nginx-served SPA),
a single fan-out HTTP entry point on port `8080`, and SQLite-by-default
(Postgres optional). First start prints a one-time admin password to
the backend logs (see compose README for the `grep` recipe).

## What it does

Sonarr will not auto-grab a season pack that contains episodes you
already have at the same quality, even if that pack also contains
*additional missing episodes* — see [Sonarr#5740](https://github.com/Sonarr/Sonarr/issues/5740),
[#6378](https://github.com/Sonarr/Sonarr/issues/6378),
[#5032](https://github.com/Sonarr/Sonarr/issues/5032). The typical
rejection looks like:

```text
Existing file on disk has a equal or higher Custom Format score: 500
Full season pack
```

The manual workaround is to open Sonarr's interactive search and use
**Override and add to Download Queue** on the same release. Seasonfill
automates that loop: it decides by *episode coverage* (not Custom
Format score), ranks candidates, and force-grabs the best one through
the same endpoint Sonarr's UI uses. The webhook receiver updates the
grab record on import success/failure and arms cooldowns to avoid
re-grabbing broken releases.

## Features

| Capability | Status |
|------------|--------|
| Multi-Sonarr instance scanning (parallel) | shipped |
| Schedule via cron + manual `POST /scan` | shipped |
| Per-instance `mode: auto\|manual` (manual = UI-only) | shipped |
| Per-instance `dry_run` (default global = true) | shipped |
| Sonarr `Connect → Webhook` receiver (Grab/Import/ImportFailed) | shipped |
| GUID + per-series cooldowns (smart) | shipped |
| Decision audit log with operator "Grab now" override | shipped |
| Operator "Rescan" with supersession chain | shipped |
| React SPA: Dashboard, Instances, Scans, Decisions, Grabs | shipped |
| Auth modes: Forms / Basic / None / OIDC (runtime-switchable) | shipped |
| OIDC SSO with PKCE + group ACL (Keycloak / Authelia / Authentik) | shipped |
| Persistent API key for Sonarr webhook (`X-Api-Key`) | shipped |
| Auto-generated first-run password (qBittorrent-style) | shipped |
| `reset-password` + `auth-mode` rescue CLI | shipped |
| Disabled-for-local-addresses bypass (RFC1918/loopback) | shipped |
| Watchdog: post-import re-grab on unregistered torrents (per-instance opt-in) | shipped |
| Media layer: TMDB-sourced poster/backdrop/cast cache backed by S3 (self-healing) | shipped |
| Helm chart (`oci://ghcr.io/alexmorbo/seasonfill-helm`) | shipped |
| Docker Compose stack | shipped |
| Prometheus `/metrics` + `ServiceMonitor` | shipped |
| Anime (absolute numbering) | **not supported** |

## Configuration overview

Bootstrap settings (database, HTTP bind, log level, encryption key,
admin user) come from environment variables — see
[`deploy/compose/.env.example`](deploy/compose/.env.example) or the
Helm `values.yaml`. Everything else (Sonarr instances, cron schedule,
dry_run, rate limits, session TTL, trusted proxies) lives in the
database and is edited via the Settings UI at `/settings`.

Sensitive values (`SEASONFILL_API_KEY`, Postgres DSN, the admin
password) come from environment variables. The Helm chart wires them
via `valueFrom.secretKeyRef` from a pre-created or chart-rendered
Secret; compose wires them from `.env`. See the deploy-path READMEs
for the exact key names.

The runtime config also carries a `guid_rewrites` table — an ordered
list of `{from, to}` substring substitutions applied to tracker GUIDs
when the UI renders "open on tracker" links. The replacements run
client-side; the database stores release GUIDs unchanged. Useful when
Sonarr is configured against a private cluster proxy
(`http://rutracker-proxy.servarr.svc.cluster.local/…`) but the UI
should show the public tracker URL.

## API surface

REST API under `/api/v1/*` (includes `/api/v1/auth/login`,
`/api/v1/webhook/sonarr/<instance-name>`, etc.). Public probes
`/healthz`, `/readyz`, `/metrics`. Every non-probe route requires
either a session cookie (UI logged in) or an `X-Api-Key` header
(Sonarr webhook, scripts).

The OpenAPI 3.0 spec is committed at
[`docs/swagger.yaml`](docs/swagger.yaml). Render it in any OpenAPI
viewer (Swagger UI, Redoc, IntelliJ HTTP client, etc.) — the service
itself does not host a live UI for the spec.

## Security model

### Authentication modes

Three modes are available, configured at runtime via **Settings →
Security** (no restart needed):

| Mode | Use case | Local bypass advisable? | Reverse proxy required? |
|------|----------|------------------------|------------------------|
| **Forms** (default) | Direct browser access, public-facing | Optional | No |
| **Basic** | CLI/scripted clients, IP-restricted deploys | Optional | Recommended for public |
| **None** | Fully behind an authenticating proxy | N/A | **Yes — mandatory** |
| **OIDC** | SSO browser auth via OIDC | Optional | Reverse proxy with TLS recommended |

> **Deployment scenarios:**
>
> | Scenario | Recommended setup |
> |----------|-------------------|
> | Local docker-compose, trusted LAN | Forms + Disabled-for-Local (defaults seeded via `.env.example`) |
> | Public via Pangolin / oauth2-proxy / Authelia | None + reverse-proxy auth |
> | Public direct | Forms + strong rotated password + HTTPS |

### Webhook invariant

`POST /api/v1/webhook/sonarr/<instance-name>` always requires the
`X-Api-Key` header regardless of auth mode or local-bypass state.
This endpoint is a public-facing surface and is never bypassed.

### Other security properties

- One admin user (username + password, bcrypt-hashed in DB).
  Auto-generated 24-char password on first start when none configured,
  printed once to logs with a `FIRST-RUN PASSWORD` banner. Docker
  Compose ships `admin/admin` via `.env.example` — **rotate on first
  login** via Settings → Security.
- Cookie HMAC-signed with the API key. `HttpOnly`, `SameSite=Strict`,
  `Secure` flag opt-in (toggle in Settings → Security when behind
  HTTPS). Mode change bumps the session epoch — all active cookies
  are invalidated immediately.
- API key persists across restarts. Rotated via Secret / `.env` edit
  + redeploy. Sonarr's `Connect → Webhook` provides it as a Custom
  header (`X-Api-Key`). Works in every auth mode.
- Rate-limited `/auth/login` (5 attempts per IP per 15min) and
  `/webhook`. Constant-time password compare. Generic error message
  ("Invalid credentials") for both unknown-user and wrong-password.
- `GET /api/v1/instances` masks Sonarr `api_key` — never returned
  by any read endpoint.
- All web responses carry a strict Content-Security-Policy plus
  `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy`, and `Permissions-Policy` (set at the nginx layer).
- CI gates image publication on dependency vulnerabilities:
  `govulncheck` (Go, reachability mode) and `npm audit` (web,
  high+). Weekly Dependabot keeps dependencies current.

See [`deploy/compose/README.md`](deploy/compose/README.md) for
runtime configuration details and the lockout rescue command.

### Configuring OIDC (Keycloak example)

1. Deploy with `oidc.enabled=true` (Helm) or `OIDC_CLIENT_SECRET=...` (compose).
2. In Keycloak, create:
   - A realm (e.g. `homelab`)
   - A client (e.g. `seasonfill`) with:
     - Access Type: confidential
     - Standard Flow Enabled
     - Valid Redirect URIs: `https://<host>/api/v1/auth/oidc/callback`
     - Web Origins: `https://<host>`
   - Copy the client secret → `OIDC_CLIENT_SECRET` env on Seasonfill
3. In Seasonfill: Settings → Security → Authentication: `OIDC`. Fill in:
   - Issuer URL: `https://<keycloak-host>/realms/homelab`
   - Client ID: `seasonfill`
   - Redirect URL: `https://<host>/api/v1/auth/oidc/callback`
   - Scopes: `openid`, `profile`, `email`
   - Username claim: `preferred_username` (default; matches Keycloak default)
   - Allowed groups: optional; leave empty for "any verified user"
4. Save. All live cookies invalidate (session epoch bump). Re-login via
   the SSO button on the login page.

### Recovery

If you lock yourself out, run:

```
seasonfill auth-mode --set forms
```

from the container shell. This restores forms-auth without clearing the
OIDC config, so you can fix the issue and switch back.

## External service credentials (TMDB / OMDb / TVDB)

API keys and proxy settings for the three enrichment sources can be set
via the Settings UI (stored AES-GCM encrypted in
`external_service_settings`) or via environment variables. Priority is
**env > DB per field** (PRD §10.4.4) — the env value overrides the UI
value when both are set, and supplies the only value when the DB row is
empty (fresh install). Setting only the env vars is sufficient for a
fresh install to boot with TMDB + OMDb enrichment fully active.

| Variable                       | DB column                  |
|--------------------------------|----------------------------|
| `SEASONFILL_TMDB_TOKEN`        | `api_key_enc` (tmdb row)   |
| `SEASONFILL_TMDB_PROXY_URL`    | `proxy_url_enc` (tmdb row) |
| `SEASONFILL_TMDB_PROXY_USER`   | `proxy_username_enc`       |
| `SEASONFILL_TMDB_PROXY_PASS`   | `proxy_password_enc`       |
| `SEASONFILL_OMDB_TOKEN`        | `api_key_enc` (omdb row)   |
| `SEASONFILL_OMDB_PROXY_URL`    | `proxy_url_enc` (omdb row) |
| `SEASONFILL_OMDB_PROXY_USER`   | `proxy_username_enc`       |
| `SEASONFILL_OMDB_PROXY_PASS`   | `proxy_password_enc`       |
| `SEASONFILL_TVDB_TOKEN`        | `api_key_enc` (tvdb row)   |
| `SEASONFILL_TVDB_PROXY_URL`    | `proxy_url_enc` (tvdb row) |
| `SEASONFILL_TVDB_PROXY_USER`   | `proxy_username_enc`       |
| `SEASONFILL_TVDB_PROXY_PASS`   | `proxy_password_enc`       |

The resolved source per field appears at boot (and after every UI
change) as a structured `extsvc.source` INFO record — one record per
service, no plaintext, only the source label + cosmetic last4:

```
extsvc.source service=tmdb enabled=true api_key=env proxy_url=db proxy_user=none proxy_pass=none last4=abcd
```

Operators can grep `kubectl logs ... | grep extsvc.source` to confirm
the fallback path is active when no UI row is present (`api_key=env`).

## Watchdog (post-import re-grab automation)

Sonarr's Failed Download Handling closes the case once an episode imports.
Anything that happens to that torrent afterwards — tracker pulls the
release because a new Proper is out, the season pack is reissued with
extra audio tracks, the announce flips to "torrent not registered" — is
invisible to Sonarr. The file on disk no longer reflects the best
release available, but nothing in the stack notices.

Watchdog closes that loop. On a configurable cadence (default 30
minutes) it polls qBittorrent per-instance for torrents whose trackers
have gone unregistered, looks up the matching grab record, re-runs the
same evaluator pipeline you use for normal scans against that
`(series, season)`, and force-grabs a better release if one exists.

### Opt-in flow

Watchdog is per-instance opt-in. Default is off; nothing changes until
you configure it for an instance. Three steps:

1. **Install the OnGrab webhook in Sonarr.** Watchdog needs the
   torrent infohash, and Sonarr delivers it on the `OnGrab` event.
   Either install it manually in Sonarr → Settings → Connect → Webhook,
   or let seasonfill do it for you:

   ```
   POST /api/v1/instances/{name}/webhook/install
   ```

   The endpoint creates a Webhook notification covering `OnGrab`,
   `OnImport`, and `OnImportFailure` with the right URL and the
   `X-Api-Key` header. Repeat calls are no-ops — it matches existing
   notifications by URL prefix.

2. **Configure qBittorrent credentials for the instance.** First call:

   ```
   GET /api/v1/instances/{name}/discover/qbit
   ```

   It reads Sonarr's `GET /api/v3/downloadclient`, finds the
   qBittorrent client, and returns host, port, username, and category.
   Sonarr never returns the password (the response is `privacy:password`
   by spec), so you supply that yourself. Then save:

   ```
   PUT /api/v1/instances/{name}/qbit/settings
   {
     "url":      "http://qbit:8080",
     "username": "admin",
     "password": "...",
     "category": "sonarr",
     "enabled":  false
   }
   ```

   The password is AES-GCM encrypted at rest before insert; read
   responses redact it.

3. **Toggle `enabled: true` with another `PUT`.** Before saving the
   backend verifies the OnGrab webhook is actually installed in Sonarr.
   If not, the call returns `409` with code `WEBHOOK_NOT_INSTALLED` and
   you go back to step 1. Once enabled, the watchdog loop picks up the
   new settings on its next wake cycle (≤2 seconds, no restart).

### Hash-required gate

Watchdog only acts on grabs that have a torrent infohash on record —
i.e. grabs made *after* you installed the OnGrab webhook for that
instance. Grabs predating the webhook stay invisible to Watchdog
forever; they remain managed by your normal scan schedule and Sonarr
itself. This is intentional: there is no backfill from Sonarr's
history (false-match risk is too high), so Watchdog coverage
accumulates naturally as new grabs flow through.

### Throttling

Three layers, all reload-bus aware (change via the API, takes effect
within ≤2 seconds, no pod restart):

| Layer | Default | Override field |
|-------|---------|----------------|
| qBit poll interval | 30 min | `poll_interval_minutes` (min 5) |
| Per-`(series, season)` re-evaluate cooldown | 120 h (5 days) | `regrab_cooldown_hours` |
| Consecutive "nothing better" auto-blacklist | 3 attempts | `max_consecutive_no_better` |

After the blacklist threshold trips, the `(instance, series, season)`
triple lands in `watchdog_blacklist` and Watchdog skips it until you
unblock manually. Persistent qBit unreachability (10 consecutive poll
failures) auto-disables the instance — credentials are wrong or the
service is down, and silently retrying forever burns evaluator
cycles.

### Security

- qBit passwords are encrypted at rest with AES-GCM, using an HKDF
  subkey domain-separated from session HMAC, OIDC client secret, and
  Sonarr API key storage. The master key respects the same env
  override path as the rest of seasonfill's at-rest secrets.
- Read responses always redact the password. There is no API to read
  it back; rotate via `PUT` instead.
- The webhook endpoint always requires `X-Api-Key` regardless of auth
  mode, including when Watchdog is doing the auto-install (see
  [§Security model](#webhook-invariant)).

### Out of scope (v1)

- No UI yet — all configuration via the REST API above.
- Only qBittorrent — Transmission/Deluge/rTorrent are not supported.
- Watchdog never writes to qBit (no tagging, no deletes). Read-only.
- Auto-unblock from the blacklist is manual; the schema reserves an
  `expires_at` column but the loop never sets it.

## Media layer

Catalog tiles, the series-detail hero backdrop, cast portraits, season
posters, episode stills, and network logos are sourced from TMDB during
enrichment and cached in an S3-compatible object store
(`mediaStore.mode=s3` — SeaweedFS and MinIO both work; `fs` mode keeps
bytes on a PVC for single-replica deploys).

The wire contract is content-addressed: every image URL the SPA renders
is `GET /api/v1/media/{hash}`, where `{hash}` is
`sha256(<full TMDB CDN URL>)`. The browser never talks to
`image.tmdb.org` directly. Bucket keys follow the same shape —
`media/v1/{hash[:2]}/{hash}.{ext}` — so two backend deployments can
share a bucket without colliding.

### Self-healing

The bucket is a **cache**, not the source of truth. The Postgres
`media_assets` table holds the upstream URL, content type, and status;
the bucket holds bytes. If an object disappears (manual wipe, hardware
loss, bucket re-creation), the next request for that hash:

1. Hits the media handler.
2. Finds the `media_assets` row → resolves the original TMDB CDN URL.
3. Re-fetches the bytes, stores them back in the bucket, serves them
   in the same response (success log: `media.serve.lost_object_recovered`).

The refetch is deduplicated per-hash via singleflight, so 100 concurrent
requests for the same missing object produce **one** upstream call.
Recovery does not require an admin action — wipe the whole bucket while
the service is running and traffic continues to serve; the bucket
repopulates lazily as users browse. Mass cold-start refills are bounded
by `SEASONFILL_TMDB_CDN_RPS`.

If TMDB itself returns 4xx for a given image (deleted upstream, never
existed), the handler serves an embedded SVG monogram placeholder with
a 5-minute browser cache rather than a 4xx. Nothing is negatively cached
server-side — every subsequent request retries the upstream until it
succeeds.

### Rate limits

Two independent budgets, both env-driven, both live-reloadable via the
reload bus (no restart needed):

| Env | Default | Used by |
|-----|---------|---------|
| `SEASONFILL_TMDB_API_RPS` | `50` | `api.themoviedb.org` (metadata during enrichment) |
| `SEASONFILL_TMDB_CDN_RPS` | `100` | `image.tmdb.org` (downloader pre-warm + on-demand recovery) |

The API and CDN budgets are split so a flood of image refetches never
starves the metadata pipeline. Legacy `SEASONFILL_TMDB_RPS` (unset by
default) is still honoured as a fallback for `*_API_RPS` if you have
existing deploys; a deprecation WARN is logged at boot.

### Pre-warming

On enrichment commit, the series worker fan-outs every known asset
variant (poster `w342`/`w780`, backdrop `w1280`, profile `w185`, etc.)
into a bounded channel; a pool of downloader goroutines drains it in
the background. Catalog list endpoints (`/series-cache`, `/missing`,
`/grabs`) also fire-and-forget an `EnsurePending` for any series whose
poster the worker hasn't reached yet — so a freshly-seen Sonarr series
renders its tile within seconds, not the next scan tick.

### Operating notes

- The bucket grows roughly **5–10 MiB per fully-hydrated series**
  (poster + backdrop + 10–15 cast portraits + episode stills). Budget
  ~10 GiB per 1 000 series.
- There is no built-in GC. The `media_assets.last_access_at` column is
  updated on every serve and reserved for a future sweep; today the
  bucket only grows.
- External TMDB / OMDb traffic is observable via
  `seasonfill_external_http_requests_total{client,endpoint,method,status}`
  and `seasonfill_external_http_request_duration_seconds{...}` on
  `/metrics` — useful to spot a recovery storm or upstream throttling.
- `DELETE FROM media_assets` together with a bucket wipe forces full
  re-hydration from TMDB on next access. Done together it's safe;
  done in isolation the rows desync but the lost-object recovery
  still fires — only `last_access_at` and `size_bytes` go stale until
  the next serve refreshes them.

## Contributing

Contributions welcome. The codebase is GPL-3.0 — fork, run, modify.
For bug reports and feature discussion, open a
[GitHub Issue](https://github.com/alexmorbo/seasonfill/issues); for
code changes, open a pull request against `main`.

### Developer setup

Pre-commit hooks gate the local workflow against regressions that the CI
later catches — `gofmt`, `go vet`, `golangci-lint`, `tsc --noEmit`, OpenAPI
drift, i18n key drift, ESLint, and `go test -short`. Enforcement is **local
only**; CI does not run pre-commit and remains the independent ground truth.

Install once after cloning:

```sh
brew install pre-commit       # or: pip install pre-commit
make pre-commit-install        # registers .git/hooks/{pre-commit,pre-push}
```

Each `git commit` then runs the fast hooks (gofmt, vet, golangci-lint over
changed packages, eslint on staged TS, tsc on web changes, OpenAPI drift
and i18n audit). Each `git push` additionally runs `go test -short ./...`
so red builds never reach the remote.

Run the full suite manually:

```sh
make pre-commit-run
```

`git commit --no-verify` is forbidden by repo policy (see CLAUDE.md
"NEVER skip hooks").

## License

[GPL-3.0](LICENSE). Forks and derivative works must remain
open-source under a compatible license.
