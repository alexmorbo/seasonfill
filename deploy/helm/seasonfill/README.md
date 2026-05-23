# seasonfill (Helm chart)

> Multi-instance Sonarr backfill controller. Singleton backend +
> React SPA, both rendered by this chart. See the project root
> [README](../../../README.md) for what the service does and why.

- **Chart version:** `0.3.0` (matches `Chart.yaml`)
- **App version:** `0.3.0`
- **OCI source:** `oci://ghcr.io/alexmorbo/seasonfill`
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
helm install seasonfill oci://ghcr.io/alexmorbo/seasonfill \
  --version 0.3.0 \
  --namespace seasonfill --create-namespace \
  --set "secrets.apiKey=$(openssl rand -hex 32)" \
  --set "instances[0].name=main" \
  --set "instances[0].url=http://sonarr.media.svc.cluster.local:8989" \
  --set "secrets.keys.sonarrApiKeyTemplate=sonarr-{name}-api-key" \
  --set "secrets.apiKey=changeme" \
  --set "secrets.webPassword=changeme"
```

The chart renders a `<release>-env` Secret from those inline values.
First-run admin password lands in the backend pod logs:

```sh
kubectl -n seasonfill logs deploy/seasonfill | grep 'FIRST-RUN PASSWORD'
```

## Install — production (existingSecret)

Recommended. Create the Secret out-of-band (sealed-secrets,
external-secrets, SOPS+Terragrunt, plain `kubectl create`) and the
chart references it. `values.yaml` stays free of sensitive material.

Secret key layout (override in `secrets.keys` if needed):

| Key | Required | When |
|-----|----------|------|
| `api-key` | yes | Always. Cookie HMAC + service-to-service auth. |
| `web-user` | no | Defaults to `admin` if missing. |
| `web-password` OR `web-password-hash` | no | If both missing → auto-gen + logs. Mutually exclusive. |
| `postgres-dsn` | conditional | Required when `config.database.driver: postgres`. |
| `sonarr-<name>-api-key` | yes (per instance) | One key per `instances[].name`. Template configurable. |

`<name>` sanitization for the Sonarr key: lowercase, runs of
non-alphanumeric → single `-`, trim ends. `main` →
`sonarr-main-api-key`. `Anime_4K` → `sonarr-anime-4k-api-key`.

Create the Secret:

```sh
kubectl -n seasonfill create secret generic seasonfill \
  --from-literal=api-key=$(openssl rand -hex 32) \
  --from-literal=web-password=changeme-on-first-login \
  --from-literal=postgres-dsn='postgres://seasonfill:pw@pg.db.svc/seasonfill?sslmode=require' \
  --from-literal=sonarr-main-api-key=$SONARR_MAIN_KEY
```

Install pointing at it:

```yaml
# values-prod.yaml
secrets:
  existingSecret: seasonfill

instances:
  - name: main
    url: http://sonarr.media.svc.cluster.local:8989

config:
  database:
    driver: postgres
  dryRun: false   # opt in to real grabs only after a dry-run scan looks correct

ingress:
  enabled: true
  className: nginx
  host: seasonfill.example.com
  tls:
    enabled: true
    secretName: seasonfill-tls
```

```sh
helm install seasonfill oci://ghcr.io/alexmorbo/seasonfill \
  --version 0.3.0 \
  --namespace seasonfill --create-namespace \
  -f values-prod.yaml
```

## Values reference (most-used)

Full reference: `helm show values oci://ghcr.io/alexmorbo/seasonfill --version 0.3.0`.

| Key | Default | Description |
|-----|---------|-------------|
| `secrets.existingSecret` | `""` | Name of pre-created Secret. When non-empty, no inline secret values may be set. |
| `secrets.keys.*` | kebab-case | Override per-key names inside the Secret. |
| `instances[]` | one `main` example | Non-secret Sonarr instance config. API key comes from the Secret. |
| `config.dryRun` | `true` | Global default. Set to `false` to opt in to real grabs. |
| `config.database.driver` | `postgres` | `sqlite` or `postgres`. SQLite path is `/data/seasonfill.db` (needs `persistence.enabled: true`). |
| `config.http.auth.sessionTTL` | `12h` | Cookie TTL. |
| `config.http.auth.secureCookie` | `false` | Set `true` only when serving HTTPS — browsers reject Secure cookies on plain HTTP. |
| `config.cron.schedule` | `0 */6 * * *` | Cron expression for the auto-scan loop. |
| `ingress.enabled` | `false` | Single fan-out mode. `/api`, `/auth`, `/webhook`, `/healthz`, `/readyz`, `/metrics` → backend; `/` → web. |
| `ingress.host` | `""` | The single host. |
| `ingress.tls.enabled` | `false` | When `true`, `ingress.tls.secretName` must reference an existing TLS Secret. |
| `persistence.enabled` | `false` | RWO PVC for SQLite mode. Ignored under Postgres. |
| `serviceMonitor.enabled` | `false` | Prometheus Operator scrape. |
| `networkPolicy.enabled` | `false` | Default-deny + explicit allow-list. |
| `web.replicaCount` | `1` | Frontend can scale horizontally (stateless). |

## Terragrunt example

Pattern: pre-create the namespace + Secret + (optional) TLS Secret,
then drive the chart via `helm_release`. SOPS-encrypted values live
outside Terraform state.

```hcl
locals {
  secrets = yamldecode(sops_decrypt_file("${get_terragrunt_dir()}/secrets.sops.yaml"))
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
    "api-key"              = local.secrets.api_key
    "web-password"         = local.secrets.web_password
    "postgres-dsn"         = local.secrets.postgres_dsn
    "sonarr-main-api-key"  = local.secrets.sonarr_main_api_key
    "sonarr-anime-api-key" = local.secrets.sonarr_anime_api_key
  }
  type = "Opaque"
}

resource "helm_release" "seasonfill" {
  name      = "seasonfill"
  chart     = "oci://ghcr.io/alexmorbo/seasonfill"
  version   = "0.3.0"
  namespace = kubernetes_namespace_v1.seasonfill.metadata[0].name

  values = [
    yamlencode({
      secrets = {
        existingSecret = kubernetes_secret_v1.seasonfill.metadata[0].name
      }

      instances = [
        {
          name = "main"
          url  = "http://sonarr.media.svc.cluster.local:8989"
          mode = "auto"
          tags = { mode = "all" }
        },
        {
          name = "anime"
          url  = "http://sonarr-anime.media.svc.cluster.local:8989"
          mode = "manual"
        },
      ]

      config = {
        dryRun = false
        database = { driver = "postgres" }
        http = { auth = { secureCookie = true } }
      }

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
kubectl -n seasonfill logs deploy/seasonfill | grep 'FIRST-RUN PASSWORD'
```

Log in with `admin` (or your `secrets.webUser`) + that password. The
UI shows a banner prompting password change; use the in-app modal to
set a new one.

To rotate later without touching the Secret:

```sh
kubectl -n seasonfill exec deploy/seasonfill -- /app/seasonfill reset-password --print
```

## Upgrades

The chart line is brand-new at `0.3.0`. There is no migration path
from earlier `0.2.x` charts — their values shape is incompatible
(`webhookOnly`, `webhookSecret`, `sonarrInstances`, `web.enabled` are
all gone). For an existing 0.2.x install, the only supported path is:

1. Capture the DB out-of-band (Postgres dump, or copy
   `/data/seasonfill.db` from the SQLite PVC).
2. `helm uninstall` the old release.
3. Create a new `Secret` per §"Install — production".
4. `helm install` 0.3.0 fresh.
5. Restore the DB into the new install (SQLite: copy the file back
   into the new PVC under the same path; Postgres: same DSN points at
   the same DB, no action needed).

Future `0.3.x → 0.3.(x+1)` upgrades are in-place: `helm upgrade` with
new values. Read the chart annotation `artifacthub.io/changes` in
`Chart.yaml` for breaking-change call-outs per release.

## Sonarr webhook configuration

In each Sonarr → Settings → Connect → + → Webhook:

- **URL:** `https://<ingress.host>/webhook`
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

## License

[GPL-3.0](../../../LICENSE).
