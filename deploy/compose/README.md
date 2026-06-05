# Seasonfill — docker-compose

> Part of [Seasonfill](../../README.md). For Kubernetes, see
> [`deploy/helm/seasonfill/README.md`](../helm/seasonfill/README.md).

Run the full stack (backend + frontend) on any Docker host. Suitable
for home labs that do not run Kubernetes. For k8s, use the Helm chart
at `deploy/helm/seasonfill/`.

## Quickstart

```sh
cd deploy/compose
cp .env.example .env
docker compose up -d
docker compose logs -f backend | grep 'FIRST-RUN'
```

Open http://localhost:8080 and log in with `admin` / `admin` (the
default credentials shipped in `.env.example`). **Change this
password immediately** via Settings → Security → Change Password, or
via the `reset-password` CLI described below.

Capture the printed `SEASONFILL_API_KEY` and paste it into your
`.env` so the next restart can decrypt the DB.

> **If you delete `SEASONFILL_WEB_PASSWORD` from `.env`** the backend
> falls back to auto-generating a 24-char password on first start and
> printing it once to the logs — same as a Kubernetes/Helm deploy.

All other settings — Sonarr instances, cron schedule, dry_run, rate
limits, session TTL, trusted proxies — are configured via the Web UI
at `/settings` once the stack is up.

## What is exposed

- Frontend container: published on host port `${WEB_PORT:-8080}` →
  container `:8080`. Serves the SPA at `/` and proxies `/api/*`,
  `/auth/*`, `/webhook`, `/readyz`, `/metrics` to the backend over
  the internal Docker network.
- Backend container: **not** published. Only reachable from the
  frontend container via `http://backend:8080` (Docker DNS).
- SQLite database: persisted in the `seasonfill-data` named volume
  (default). Use `docker volume inspect seasonfill_seasonfill-data`
  to find its on-disk path.

## Sonarr webhook configuration

In Sonarr → Settings → Connect → + → Webhook:

- **URL:** `http://<your-host>:8080/api/v1/webhook/sonarr/<instance-name>`
  (replace `<instance-name>` with the name you gave the instance when
  adding it via the seasonfill Settings UI at `/settings`)
- **Method:** POST
- **Triggers:** at minimum `On Grab`, `On Import`, `On Import Failure`
- **Custom Headers:** add one header
  - Name: `X-Api-Key`
  - Value: the same value as `SEASONFILL_API_KEY` in `.env`

Sonarr sends events to seasonfill; the backend records them and uses
them to drive cooldowns + grab decisions.

## Database options

- **SQLite (default):** zero config, persisted in the
  `seasonfill-data` named volume. Fine for single-host hobby use.
- **Postgres:** set `SEASONFILL_DATABASE_DRIVER=postgres` and a full
  `SEASONFILL_DATABASE_POSTGRES_DSN` in `.env`. seasonfill auto-migrates
  the schema at startup.

## Reverse proxy & TLS

The compose stack speaks plain HTTP on port 8080. To put it behind
TLS, terminate at your own reverse proxy:

- **Caddy:** one-liner — `seasonfill.example.com { reverse_proxy
  localhost:8080 }`. Caddy handles ACME automatically.
- **Traefik:** label the frontend service with the usual host rule +
  cert resolver.
- **nginx/HAProxy:** standard reverse proxy config to
  `http://127.0.0.1:8080`.

When terminating TLS upstream, toggle "Secure cookie" in the Web UI
under Settings → Auth & Security so the session cookie carries the
`Secure` flag. Do NOT flip this to true while still serving plain
HTTP — browsers refuse Secure cookies over HTTP and login will
silently fail.

## Authentication modes

Auth mode is a runtime setting — change it in the web UI at
**Settings → Security** with no restart required.

| Mode | Description | When to use |
|------|-------------|-------------|
| **Forms** (default) | Username/password via the web login page; session cookie issued on success. | Recommended for direct browser access or any public-facing deploy. |
| **Basic** | HTTP Basic Auth — the browser shows a native credentials popup; no login page rendered. | Useful for CLI/scripted clients or simple setups behind an IP allowlist. |
| **None** | No authentication enforced by seasonfill. | **WARNING:** use ONLY behind a reverse proxy that authenticates (Pangolin, oauth2-proxy, Authelia, etc.). Enabling None without a protecting proxy exposes the entire UI to anyone. |
| **OIDC** | SSO via an external OpenID Connect provider (Keycloak, Authelia, Authentik). | Recommended for shared / public deploys with an existing IdP. See §"OIDC mode" for setup. |

### Authentication Required toggle

The "Authentication Required" dropdown offers an optional
**Disabled for Local Addresses** mode: requests originating from
local CIDRs skip auth entirely regardless of the mode setting above.

Default local network list (editable in the UI):

- `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` — RFC1918 private
- `127.0.0.0/8` — IPv4 loopback
- `::1/128` — IPv6 loopback
- `169.254.0.0/16` — IPv4 link-local
- `fe80::/10` — IPv6 link-local
- `fc00::/7` — IPv6 ULA

> **Webhook invariant:** `POST /api/v1/webhook/*` always requires
> `X-Api-Key` regardless of auth mode or local-bypass state. The
> webhook endpoint is a public-facing surface that must not be
> bypassed.

### Lockout rescue

If you accidentally lock yourself out (e.g. set mode=None on a
public surface and now need to restore Forms), reset via the CLI:

```sh
docker compose exec backend /app/seasonfill auth-mode --set forms
```

This writes directly to the DB, bumps the session epoch (invalidates
all active cookies), and exits. The next page load will show the
login form again.

## Operations

### Tail logs

```sh
docker compose logs -f backend
docker compose logs -f frontend
```

### Reset the admin password

```sh
docker compose exec backend /app/seasonfill reset-password --print
```

Prints a fresh random password to stdout (also persisted as a bcrypt
hash in the DB). Use `--set <password>` to choose a specific one.

### Pin to a specific image tag

Set `TAG=` in `.env` to a published image tag (e.g. `TAG=0.6.0`).
Default `latest` floats with the newest published image. Production
deployments should pin.

### Stop & remove

```sh
docker compose down            # keeps the SQLite volume
docker compose down -v         # wipes the SQLite volume too
```

## OIDC mode

Seasonfill can delegate browser authentication to an external OpenID Connect
provider (Keycloak, Authelia, Authentik). Automation clients (webhooks,
scripts using X-Api-Key) continue to work unchanged.

### Setup

1. Provision the OIDC client in your provider. Redirect URI must match
   Seasonfill's `redirect_url` exactly. For local docker-compose, that's
   `http://localhost:8080/api/v1/auth/oidc/callback`.
2. Copy the client secret into a `.env` next to the compose file:
   `OIDC_CLIENT_SECRET=...`. Uncomment the `OIDC_CLIENT_SECRET:` env line
   in `docker-compose.yaml`.
3. `docker compose up -d`.
4. Open the Seasonfill UI, go to Settings → Security, switch the
   Authentication dropdown to `OIDC`, fill in issuer URL, client ID,
   redirect URL, scopes, and username claim. Save.

The example `docker-compose.yaml` contains a commented Keycloak side-service
(`quay.io/keycloak/keycloak:25.0`) you can uncomment for testing.

### Recovery

If you lock yourself out (e.g. wrong issuer URL or revoked provider):

```
docker exec seasonfill-backend seasonfill auth-mode --set forms
```

This flips the backend back to forms-auth without touching the OIDC config —
the values are still there when you flip back.

## Troubleshooting

- **Login page loads but credentials are rejected.** Tail
  `docker compose logs backend | grep 'FIRST-RUN PASSWORD'` again, or
  reset via `reset-password --print`.
- **Webhook returns 401.** Verify the Sonarr Custom Header is
  literally `X-Api-Key` and the value matches `SEASONFILL_API_KEY`.
- **Frontend up, /api returns 502.** Backend likely crashed. Check
  `docker compose logs backend` for config errors (missing
  `SONARR_MAIN_URL` etc.).
- **Cookie not set after login (over HTTPS).** Confirm the
  "Secure cookie" toggle is on in Settings → Auth & Security AND
  your reverse proxy forwards `X-Forwarded-Proto: https`.
