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

## Contributing

Contributions welcome. The codebase is GPL-3.0 — fork, run, modify.
For bug reports and feature discussion, open a
[GitHub Issue](https://github.com/alexmorbo/seasonfill/issues); for
code changes, open a pull request against `main`.

### Developer setup

Pre-commit hooks catch `gofmt`, `go vet`, and ESLint regressions before
you commit. Enforcement is **local only** — CI does not run pre-commit;
install the hook on your checkout to get the benefit.

```sh
pip install pre-commit        # or: brew install pre-commit
pre-commit install            # registers .git/hooks/pre-commit
```

Every `git commit` then runs the configured hooks against staged files.
To run the full suite manually:

```sh
pre-commit run --all-files
```

## License

[GPL-3.0](LICENSE). Forks and derivative works must remain
open-source under a compatible license.
