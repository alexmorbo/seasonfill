# seasonfill (Helm chart)

> Multi-instance Sonarr backfill controller. Singleton backend +
> React SPA, both rendered by this chart. See the project root
> [README](../../../README.md) for what the service does and why.

- **Chart version:** `0.6.2` (matches `Chart.yaml`)
- **App version:** `0.6.2`
- **OCI source:** `oci://ghcr.io/alexmorbo/seasonfill-helm`
- **Kubernetes:** `>=1.25.0`
- **Helm:** `>=3.16` (OCI install support stable, schema validation)

## Prerequisites

- A Kubernetes 1.25+ cluster with cluster-admin (or namespace-admin
  with permission to create `Deployment`, `Service`, `ConfigMap`,
  `Secret`, `Ingress`, optional `NetworkPolicy` + `ServiceMonitor`).
- An Ingress controller already installed in the cluster (`ingress-nginx`,
  `traefik`, etc.). The chart only renders an `Ingress` object —
  bring your own controller.
- For TLS: either cert-manager (the chart references an existing TLS
  Secret name via `ingress.tls.secretName`) or terminate TLS at a
  layer upstream of the Ingress (per-cluster choice).
- For Postgres mode: a reachable Postgres 14+ server. The chart does
  NOT provision Postgres (deliberately — bring your own CNPG /
  cloud-managed instance).

## Install — quick (inline secret, dev / CI only)

Suitable for one-off smoke tests. Inline values land in
`helm_release` Terraform state as plaintext — do not use this in
production with Terragrunt.

```sh
helm install seasonfill oci://ghcr.io/alexmorbo/seasonfill-helm \
  --version 0.6.2 \
  --namespace seasonfill --create-namespace \
  --set "database.driver=sqlite" \
  --set "persistence.enabled=true"
```

That's it — no Secret values, no instances. The backend
auto-generates the API key and admin password on first start. Grab
them once from the logs:

```sh
kubectl -n seasonfill logs deploy/seasonfill | grep 'FIRST-RUN'
```

Capture the API key — you MUST set `SEASONFILL_API_KEY` (via the
Secret) on every subsequent restart, otherwise the process aborts
(the DB holds AES-GCM-encrypted Sonarr instance secrets that need
this key to decrypt).

Then open the Settings UI at `/settings` and add your Sonarr instances.

## Install — production (existingSecret)

Recommended. Create the Secret out-of-band (sealed-secrets,
external-secrets, SOPS+Terragrunt, plain `kubectl create`) and the
chart references it. `values.yaml` stays free of sensitive material.

Secret key layout (override in `secrets.keys` if needed):

| Key | Required | When |
|-----|----------|------|
| `api-key` | no (recommended yes) | First start auto-generates if missing — capture from logs and feed back via the Secret on every restart after that. |
| `web-user` | no | Defaults to `admin` if missing. Bootstrap-only — change in the UI later. |
| `web-password` OR `web-password-hash` | no | If both missing → auto-gen + logs. Mutually exclusive. |
| `postgres-dsn` | conditional | Required when `database.driver: postgres`. |

Sonarr instance API keys are no longer kept as Secret entries. They
live AES-GCM-encrypted in the DB, added via the Settings UI.

Create the Secret:

```sh
kubectl -n seasonfill create secret generic seasonfill \
  --from-literal=api-key=$(openssl rand -hex 32) \
  --from-literal=web-password=changeme-on-first-login \
  --from-literal=postgres-dsn='postgres://seasonfill:pw@pg.db.svc/seasonfill?sslmode=require'
```

Install pointing at it:

```yaml
# values-prod.yaml
secrets:
  existingSecret: seasonfill

database:
  driver: postgres

ingress:
  enabled: true
  className: nginx
  host: seasonfill.example.com
  tls:
    enabled: true
    secretName: seasonfill-tls
```

```sh
helm install seasonfill oci://ghcr.io/alexmorbo/seasonfill-helm \
  --version 0.6.2 \
  --namespace seasonfill --create-namespace \
  -f values-prod.yaml
```

Log in to `/settings` and add your Sonarr instances. They persist
in the DB; no chart re-render required.

## Values reference (most-used)

Full reference: `helm show values oci://ghcr.io/alexmorbo/seasonfill-helm --version 0.6.2`.

| Key | Default | Description |
|-----|---------|-------------|
| `secrets.existingSecret` | `""` | Name of pre-created Secret. When non-empty, no inline secret values may be set. |
| `secrets.keys.*` | kebab-case | Override per-key names inside the Secret. |
| `database.driver` | `postgres` | `sqlite` or `postgres`. SQLite path is `/data/seasonfill.db` (needs `persistence.enabled: true`). |
| `database.sqlite.path` | `/data/seasonfill.db` | Used only when `driver=sqlite`. |
| `log.level` | `info` | `debug` / `info` / `warn` / `error`. |
| `log.format` | `json` | `json` / `text`. |
| `http.bind` | `:8080` | Listen address. |
| `ingress.enabled` | `false` | Single fan-out mode. `/api`, `/auth`, `/webhook`, `/healthz`, `/readyz`, `/metrics` → backend; `/` → web. |
| `ingress.host` | `""` | The single host. |
| `ingress.tls.enabled` | `false` | When `true`, `ingress.tls.secretName` must reference an existing TLS Secret. |
| `persistence.enabled` | `false` | RWO PVC for SQLite mode. Ignored under Postgres. |
| `serviceMonitor.enabled` | `false` | Prometheus Operator scrape. |
| `networkPolicy.enabled` | `false` | Default-deny + explicit allow-list. |
| `web.replicaCount` | `1` | Frontend can scale horizontally (stateless). |
| `mediaStore.mode` | `off` | `s3` (recommended, SeaweedFS / MinIO) or `fs` (single-replica) enables the cached media path. |
| `mediaStore.s3.endpoint` | `""` | S3 endpoint URL (e.g. `https://s3.morbo.dev` or `http://seaweedfs-s3.seaweedfs.svc:8333`). |
| `mediaStore.s3.bucket` | `seasonfill-media` | Bucket name. Created out-of-band; the chart does not provision it. |
| `mediaStore.s3.existingSecret` | `""` | Secret with `access-key` + `secret-key`. Key names overridable via `mediaStore.s3.secretKeys`. |
| `mediaStore.fs.path` | `/data/media` | Local store mount path when `mode=fs`. |
| `mediaStore.fs.persistence.enabled` | `false` | RWO PVC for `fs` mode. |

Everything else — cron schedule, scan tuning, `dry_run`, instances,
session TTL, secure cookie toggle, trusted proxies, **auth mode** —
is managed via the Settings UI at `/settings`. Not in the chart values.

**Auth mode** defaults to `forms` on first boot (username + password
login page). To change it after deploy, open Settings → Security and
select Forms / Basic / None from the dropdown — no restart or chart
upgrade required. For a CLI fallback (e.g. lockout recovery):

```sh
kubectl -n seasonfill exec deploy/seasonfill -- /app/seasonfill auth-mode --set forms
```

## Terraform example

Pattern: pre-create the namespace + Secret + (optional) TLS Secret,
then drive the chart via `helm_release`. Secret values come in as
sensitive variables so they stay out of `.tf` files.

```hcl
variable "api_key" {
  type      = string
  sensitive = true
}

variable "web_password" {
  type      = string
  sensitive = true
}

variable "postgres_dsn" {
  type      = string
  sensitive = true
}

resource "kubernetes_namespace_v1" "seasonfill" {
  metadata { name = "seasonfill" }
}

resource "kubernetes_secret_v1" "seasonfill" {
  metadata {
    name      = "seasonfill"
    namespace = kubernetes_namespace_v1.seasonfill.metadata[0].name
  }
  data = {
    "api-key"      = var.api_key
    "web-password" = var.web_password
    "postgres-dsn" = var.postgres_dsn
  }
  type = "Opaque"
}

resource "helm_release" "seasonfill" {
  name      = "seasonfill"
  chart     = "oci://ghcr.io/alexmorbo/seasonfill-helm"
  version   = "0.6.2"
  namespace = kubernetes_namespace_v1.seasonfill.metadata[0].name

  values = [
    yamlencode({
      secrets = {
        existingSecret = kubernetes_secret_v1.seasonfill.metadata[0].name
      }

      database = { driver = "postgres" }

      ingress = {
        enabled   = true
        className = "nginx"
        host      = "seasonfill.example.com"
        tls = {
          enabled    = true
          secretName = "seasonfill-tls"
        }
      }

      serviceMonitor = { enabled = true }
      networkPolicy  = { enabled = true }
    }),
  ]
}
```

Note: Sonarr instances are no longer in the chart values — log in to
the Settings UI at `/settings` after first install to add them.

Why `existingSecret`: `helm_release.values` are stored verbatim in
Terraform state (no auto-masking for sensitive substrings). Wiring
the Secret via a separate `kubernetes_secret_v1` keeps state holding
only the Secret *name*, not its data.

## First-run

If neither `secrets.webPassword` nor `secrets.webPasswordHash` is
provided, the backend auto-generates a 24-char password on its first
boot against an empty DB, bcrypts it, persists the hash, and prints
the plaintext **once** to logs:

```sh
kubectl -n seasonfill logs deploy/seasonfill | grep 'FIRST-RUN'
```

The same first-start sequence also auto-generates `SEASONFILL_API_KEY`
if missing — capture both from the log lines and feed the API key back
via the Secret for every subsequent restart.

Log in with `admin` (or your `secrets.webUser`) + that password. The
UI shows a banner prompting password change; use the in-app modal to
set a new one.

To rotate later without touching the Secret:

```sh
kubectl -n seasonfill exec deploy/seasonfill -- /app/seasonfill reset-password --print
```

## Upgrades

**`0.6.1 → 0.6.2` — in-place.** Security-only release. Bumps three
indirect Go deps (`jackc/pgx/v5`, `quic-go`, `edwards25519`) and
moves the web image base from `nginx-unprivileged:1.27-alpine`
(alpine 3.21) to `1.29-alpine3.23`, closing ~89 alpine CVEs
including a critical libcrypto3/libssl3 issue and a critical pgx
CVE on the backend. No values changes. `helm upgrade` is safe.

**`0.6.0 → 0.6.1` — in-place.** Web image now bakes the real release
version into the SPA's TopBar label instead of the raw 40-char git
SHA. No values changes. `helm upgrade` is safe.

**`0.5.x → 0.6.0` — in-place.** Polish + fix release: grab counter
desync fixed, branding refresh, Russian README, dependency bumps.
No values changes.

**`0.4.x → 0.5.0` — in-place.** Adds the optional `oidc.*` values
tree and (optional) `oidc-client-secret` Secret key. Nothing existing
is renamed or removed; running `helm upgrade` with your current
values is safe. To enable OIDC after upgrading, see §"OIDC mode".

**`0.3.x → 0.4.x` — destroy-and-recreate.** Values shape is
incompatible (`config.*`, `instances[]`, and `sonarr-<name>-api-key`
Secret keys are all gone — runtime config lives in the DB now). To
migrate:

1. Capture the DB out-of-band (Postgres dump or copy
   `/data/seasonfill.db` from the SQLite PVC).
2. `helm uninstall` the old release.
3. Create a new Secret per §"Install — production" (omit
   `sonarr-*-api-key` entries — instances now live in the DB).
4. `helm install` `0.6.2` fresh.
5. Restore the DB if needed; re-add Sonarr instances in the UI.

## Sonarr webhook configuration

Add the instance in the seasonfill Settings UI first (`/settings`).
Then in Sonarr → Settings → Connect → + → Webhook:

- **URL:** `https://<ingress.host>/api/v1/webhook/sonarr/<instance-name>`
  where `<instance-name>` matches the name you gave the instance in
  the Settings UI (unknown names return 404).
- **Method:** POST
- **Triggers:** at minimum `On Grab`, `On Import Complete`,
  `On Manual Interaction Required`
- **Custom Header:**
  - Name: `X-Api-Key`
  - Value: the same value as the `api-key` Secret entry

Click **Test** → expect `200 OK`. After the next force-grab, watch
the backend logs for `webhook_event_received` followed by
`webhook_event_imported` (success) or `webhook_event_import_failed`
(failure → 48h GUID cooldown).

## Configuring watchdog

The Watchdog feature (post-import torrent re-grab) has **no chart
values**. All configuration lives in the running app's database and
is set per-instance via the REST API after first start. The chart
neither exposes Watchdog toggles nor mounts qBit credentials —
nothing to set in `values.yaml`.

The K8s deploy specifics: typically Sonarr lives in the same cluster
and reaches seasonfill via the in-cluster Service DNS name; the
webhook URL the auto-installer writes to Sonarr is built from your
configured public URL (the same one used for the `Ingress`):

```
<seasonfill.public_url>/api/v1/webhook/sonarr/<instance-name>
```

Setup, after the pod is running and you have a Sonarr instance
configured in the Settings UI:

1. **Install the OnGrab webhook in Sonarr.** Run from any host with
   network access to seasonfill — e.g. `kubectl port-forward
   svc/seasonfill 8080:8080` plus:

   ```sh
   curl -X POST -H "X-Api-Key: $API_KEY" \
     http://localhost:8080/api/v1/instances/<name>/webhook/install
   ```

2. **Autofill qBit credentials from Sonarr.**

   ```sh
   curl -H "X-Api-Key: $API_KEY" \
     http://localhost:8080/api/v1/instances/<name>/discover/qbit
   ```

3. **Save qBit settings, then enable.** Use `PUT
   /api/v1/instances/<name>/qbit/settings` with the discovered fields
   plus the password. First save with `enabled: false`, then a second
   `PUT` flips it to `true`. The toggle is rejected with `409
   WEBHOOK_NOT_INSTALLED` if step 1 was skipped.

Reload-bus aware: changing `poll_interval_minutes`,
`regrab_cooldown_hours`, or `enabled` via the API takes effect within
≤2 seconds with no pod restart. Watchdog metrics
(`seasonfill_watchdog_*`) are exported on `/metrics` and scraped by
`ServiceMonitor` automatically when `serviceMonitor.enabled: true`.

The full Watchdog model (opt-in flow, hash-required gate, throttling
layers, security) is documented in the project root
[README.md](../../../README.md#watchdog-post-import-re-grab-automation).

## Media store

The catalog UI serves posters, backdrops, cast portraits, season posters,
episode stills, and network logos through `GET /api/v1/media/{hash}`. The
bytes are sourced from TMDB during enrichment and cached in an
S3-compatible store; the browser never reaches `image.tmdb.org` directly.

**Bucket pre-provisioning.** The chart does not create the bucket — make
sure it exists before flipping `mediaStore.mode` to `s3`. SeaweedFS:

```sh
kubectl -n seaweedfs exec deploy/seaweedfs-s3 -- \
  s3api create-bucket --bucket seasonfill-media
```

MinIO: use `mc mb` against your endpoint.

**Credentials Secret.** Create a Secret with two keys (`access-key`,
`secret-key`) referenced by `mediaStore.s3.existingSecret`:

```sh
kubectl -n seasonfill create secret generic seasonfill-s3-creds \
  --from-literal=access-key=<AK> \
  --from-literal=secret-key=<SK>
```

Then in values:

```yaml
mediaStore:
  mode: s3
  s3:
    endpoint: http://seaweedfs-s3.seaweedfs.svc:8333
    bucket: seasonfill-media
    useSSL: false
    existingSecret: seasonfill-s3-creds
```

**Self-healing.** The bucket is a cache. If you wipe it (or it dies),
the next request for each hash refetches the bytes from TMDB and writes
them back. No admin action needed; concurrent requests for the same
missing object collapse into a single upstream call (singleflight).
Successful recoveries log `media.serve.lost_object_recovered` at WARN —
a heartbeat to alert on if the rate is unexpectedly high.

**Media fetch tuning (W19-1).** Cold-series media fills are **uncapped by
default** — `image.tmdb.org` is Cloudflare-backed with no published per-IP
limit, so seasonfill downloads with a wide worker pool and no self-imposed
rate cap. Three env knobs allow rollback without a rebuild:

| Env | Default | Effect |
|-----|---------|--------|
| `SEASONFILL_MEDIA_DOWNLOADER_WORKERS` | `32` | Downloader drain-goroutine pool size. |
| `SEASONFILL_TMDB_CDN_RPS` | unset → **uncapped** (`rate.Inf`) | Set a positive value to re-impose a finite CDN rate cap. |
| `SEASONFILL_MEDIA_ONDEMAND_BUDGET` | `10` (seconds, floor 1 s) | Single source of truth for the on-demand fetch budget — drives BOTH the handler wall budget and the fetcher floor timeout. |

**Capacity planning.** ~5–10 MiB per fully-hydrated series. Budget
~10 GiB per 1 000 series. No built-in GC yet — the bucket only grows.

The full architecture (wire contract, pre-warm pipeline, rate-limit
budgets, operating notes) is in the project root
[README.md](../../../README.md#media-layer).

## Troubleshooting

- **Login page rejects credentials.** Re-grep the logs for the
  `FIRST-RUN PASSWORD` line, or run `reset-password --print`.
- **Webhook returns 401.** Verify Sonarr's Custom Header is literally
  `X-Api-Key` and the value matches the `api-key` entry of the Secret.
- **Pods stuck `ContainerCreating` with `failed to find secret`.**
  Confirm `secrets.existingSecret` matches the Secret name in the
  same namespace, and that every required key (per the §"Install —
  production" table) is present.
- **`helm install` fails with `values.schema.json` violations.**
  The schema enforces required+mutex constraints
  (`existingSecret` xor inline `secrets.apiKey`;
  `webPassword` xor `webPasswordHash`). The error message names the
  offending path.

## OIDC mode

To run Seasonfill behind an OpenID Connect provider (Keycloak, Authelia,
Authentik):

1. Set `oidc.enabled: true` in your values.
2. Provide the client secret either inline (`secrets.oidcClientSecret`) or
   via an existing Secret with key `oidc-client-secret`.
3. Deploy.
4. Open the UI, go to Settings → Security, switch the Authentication
   dropdown to `OIDC`, and fill in issuer URL, client ID, redirect URL,
   scopes, username claim, and (optionally) allowed groups.
5. Save. All live cookies invalidate; the next request triggers the OIDC
   flow.

If you lock yourself out (e.g. wrong issuer URL after a deploy), use the
rescue command:

```
kubectl exec deploy/seasonfill -- seasonfill auth-mode --set forms
```

This flips the backend back to forms-auth mode without touching the OIDC
config (so the values are still there when you flip back).

## License

[GPL-3.0](../../../LICENSE).
