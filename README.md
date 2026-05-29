# Seasonfill

A companion service for [Sonarr](https://sonarr.tv) that automates
grabbing updated season packs when Sonarr's native upgrade logic
refuses to. Multi-instance, scheduled, with a webhook receiver that
closes the import-success/failure loop and a small React UI for
operator override.

> **Project status: alpha.** Works on the author's homelab. Breaking
> changes (config schema, chart values shape, DB columns) are likely
> until `v1.0`. Pin image tags in production. Not accepting external
> contributions yet — see [Contributing](#contributing).

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
| Username + password admin login + persistent API key | shipped |
| Auto-generated first-run password (qBittorrent-style) | shipped |
| `reset-password` CLI | shipped |
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

- One admin user (username + password, bcrypt-hashed in DB).
  Auto-generated 24-char password on first start when none configured,
  printed once to logs with a `FIRST-RUN PASSWORD` banner.
- Cookie HMAC-signed with the API key. `HttpOnly`, `SameSite=Strict`,
  `Secure` flag opt-in (`http.auth.secure_cookie: true` when behind
  HTTPS).
- API key persists across restarts. Rotated via Secret / `.env` edit
  + redeploy. Sonarr's `Connect → Webhook` provides it as a Custom
  header (`X-Api-Key`).
- Rate-limited `/auth/login` (5 attempts per IP per 15min) and
  `/webhook`. Constant-time password compare. Generic error message
  ("Invalid credentials") for both unknown-user and wrong-password.
- `GET /api/v1/instances` masks Sonarr `api_key` — never returned
  by any read endpoint.
- CI gates image publication on dependency vulnerabilities:
  `govulncheck` (Go, reachability mode) and `npm audit` (web,
  high+). Weekly Dependabot keeps dependencies current.

## Contributing

Single-maintainer project — **not accepting external pull requests
yet**. The codebase is open under GPL-3.0 so you can fork, run, and
modify it freely. Bug reports and feature discussion: open a
[GitHub Issue](https://github.com/alexmorbo/seasonfill/issues).

## License

[GPL-3.0](LICENSE). Forks and derivative works must remain
open-source under a compatible license.
