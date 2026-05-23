# Seasonfill — docker-compose

Run the full stack (backend + frontend) on any Docker host. Suitable
for home labs that do not run Kubernetes. For k8s, use the Helm chart
at `deploy/helm/seasonfill/`.

## Quickstart

```sh
cd deploy/compose
cp .env.example .env
cp config.example.yaml config.yaml

# Generate the API key (32-byte hex):
openssl rand -hex 32
# Paste into SEASONFILL_API_KEY=... in .env

# Edit .env: set SONARR_MAIN_URL and SONARR_MAIN_API_KEY.
# (config.yaml interpolates them at startup — no need to edit it for
# the simple single-instance case.)

docker compose up -d
docker compose logs -f backend | grep 'FIRST-RUN PASSWORD'
```

Open http://localhost:8080 and log in with `admin` + the password
from the logs. The UI shows a banner prompting you to change the
auto-generated password — do so on first login.

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

- **URL:** `http://<your-host>:8080/webhook` (or your reverse proxy URL)
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
- **Postgres:** set `DB_DRIVER=postgres` and a full `POSTGRES_DSN` in
  `.env`. seasonfill auto-migrates the schema at startup.

## Reverse proxy & TLS

The compose stack speaks plain HTTP on port 8080. To put it behind
TLS, terminate at your own reverse proxy:

- **Caddy:** one-liner — `seasonfill.example.com { reverse_proxy
  localhost:8080 }`. Caddy handles ACME automatically.
- **Traefik:** label the frontend service with the usual host rule +
  cert resolver.
- **nginx/HAProxy:** standard reverse proxy config to
  `http://127.0.0.1:8080`.

When terminating TLS upstream, set `SEASONFILL_AUTH_SECURE_COOKIE=true`
in `.env` so the session cookie carries the `Secure` flag. Do NOT
flip this to true while still serving plain HTTP — browsers refuse
Secure cookies over HTTP and login will silently fail.

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

Set `TAG=` in `.env` to a published chart version (e.g.
`TAG=0.3.0`). Default `latest` floats with the newest published
image. Production deployments should pin.

### Stop & remove

```sh
docker compose down            # keeps the SQLite volume
docker compose down -v         # wipes the SQLite volume too
```

## Troubleshooting

- **Login page loads but credentials are rejected.** Tail
  `docker compose logs backend | grep 'FIRST-RUN PASSWORD'` again, or
  reset via `reset-password --print`.
- **Webhook returns 401.** Verify the Sonarr Custom Header is
  literally `X-Api-Key` and the value matches `SEASONFILL_API_KEY`.
- **Frontend up, /api returns 502.** Backend likely crashed. Check
  `docker compose logs backend` for config errors (missing
  `SONARR_MAIN_URL` etc.).
- **Cookie not set after login (over HTTPS).** Confirm
  `SEASONFILL_AUTH_SECURE_COOKIE=true` AND your reverse proxy
  forwards `X-Forwarded-Proto: https`.
