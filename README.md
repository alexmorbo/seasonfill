# Seasonfill

A companion service for [Sonarr](https://sonarr.tv) that automates grabbing
updated season packs when Sonarr's native upgrade logic refuses to.

> 🚧 **Early development — no usable release yet.** Active design phase.

## The problem

Sonarr will not auto-grab a season pack that contains episodes you already have
at the same quality, even if that pack also contains *additional missing
episodes*. This is intentional behavior — see
[Sonarr#5740](https://github.com/Sonarr/Sonarr/issues/5740),
[#6378](https://github.com/Sonarr/Sonarr/issues/6378),
[#5032](https://github.com/Sonarr/Sonarr/issues/5032) — but it blocks the very
common case where a partial season was grabbed early and a later-published
full pack would fill in the missing episodes.

The typical rejection looks like this:

```
Existing file on disk has a equal or higher Custom Format score: 500
Full season pack
```

You end up doing it by hand: open interactive search every few days, find the
same release on the tracker, and use **Override and add to Download Queue**.
Seasonfill automates that loop.

## The approach

Decide by *episode coverage*, not by Custom Format score:

1. Find series with monitored-but-missing episodes.
2. Query Prowlarr via Sonarr's release API.
3. Rank candidates by CF score → coverage → origin-release stickiness →
   indexer priority → seeders → size.
4. Force-grab the best one through the same endpoint Sonarr's UI uses for
   *Override and add to Download Queue*.

The algorithm avoids the recursive deadlock the CF-score workaround eventually
hits (see design discussion).

## Safe by default

Seasonfill ships with `dry_run: true`. On first run it scans your Sonarr
instances and logs the season packs it *would* grab, but it does NOT issue
any `POST /api/v3/release` calls. Inspect the decisions in the logs (or in
the `decisions` DB table), confirm they look right, then opt in to real
grabs by setting `dry_run: false` — globally or per-instance:

```yaml
dry_run: false                # global opt-in
sonarr_instances:
  - name: sonarr-main
    # this instance now grabs for real
  - name: sonarr-4k
    dry_run: true             # keep this one in dry-run
```

Instance overrides win over the global flag, so rollouts can proceed one
Sonarr at a time. See `documentation/00-design-thoughts.md` §7.1 for the
full design rationale.

## Webhook setup

Seasonfill receives a Sonarr Connect → Webhook callback so it can close
the loop on each force-grab: `grabbed → imported` on success, `grabbed
→ import_failed` (plus a 48h guid cooldown) on failure. Without the
webhook `grab_records` rows stay in `grabbed` forever and the
import-failed cooldown never fires — seasonfill may re-grab the same
broken release on the next scan.

### 1. Deploy with the webhook enabled

Use a webhook secret distinct from `.Values.secrets.apiKey` — the
admin key and webhook secret authenticate disjoint surfaces (the 007c1
isolation fix mounts the webhook route outside the admin auth group).

```yaml
# values.yaml
secrets:
  apiKey: "<long-random-admin-key>"
  webhookSecret: "<different-long-random-webhook-key>"

config:
  webhook:
    # Must contain every sonarrInstances[].name you point Sonarr at.
    # Mismatched names return 404.
    allowedInstances: [main]

ingress:
  enabled: true
  hosts:
    - host: seasonfill.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: seasonfill-tls
      hosts: [seasonfill.example.com]
  # Optional: restrict the public Ingress to ONLY /api/v1/webhook
  # and keep admin routes reachable only from inside the cluster.
  # webhookOnly: true

networkPolicy:
  enabled: true
  ingress:
    from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: ingress-nginx
  # Allow Sonarr's namespace to reach :8080 directly (in-cluster,
  # bypassing the Ingress). Either ingress path is acceptable.
  webhookSources:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: sonarr
```

Empty `webhookSecret` is legal: the 007c handler logs
`webhook_auth_disabled` once at startup and accepts any source. In
that mode the NetworkPolicy `webhookSources` allow-list is the only
gate — do NOT combine empty `webhookSecret` with a public Ingress.

### 2. Configure Sonarr Connect → Webhook

In each Sonarr instance: **Settings → Connect → + → Webhook**.

**Name**: e.g., `Seasonfill`.

**Notification Triggers** — enable exactly these three; everything
else is silently dropped by the 007a mapper:

- [x] **On Grab** — forward-compat; logged and dropped today.
- [x] **On Import Complete** *(Sonarr v4)* — triggers
      `grabbed → imported`.
- [x] **On Manual Interaction Required** — triggers
      `grabbed → import_failed` + 48h guid cooldown. Internally also
      aliased to `DownloadFailure` / `ImportFailure` for older Sonarr
      builds.

**URL**: `https://seasonfill.example.com/api/v1/webhook/sonarr/main`
(replace `main` with the matching `sonarrInstances[].name` — the URL
path is the trust boundary, not the JSON body's `instanceName`).
**Method**: `POST`. **Username/Password**: leave blank.

**Headers** *(Sonarr v4+)* — add one custom header:

| Name | Value |
|------|-------|
| `X-Api-Key` | the value of `.Values.secrets.webhookSecret` |

Older Sonarr builds without a Headers tab must rely on NetworkPolicy
(`webhookSources`) as the gate.

Click **Test** → expect `200 OK {"ok": true}` and a green checkmark.
**Save**.

### 3. Verify end-to-end

After the next force-grab, watch the seasonfill logs for a
`webhook_event_received` line followed by `webhook_event_imported`
(success) or `webhook_event_import_failed` (failure → 48h guid
cooldown). The `grab_records` row transitions to the terminal state.

If the trigger never fires: check Sonarr's Settings → Connect →
Webhook → row → Errors tab; confirm the URL `:instance_name` matches
both `.Values.config.webhook.allowedInstances` and a
`sonarrInstances[].name` (mismatch returns 404); confirm the
`X-Api-Key` header matches `.Values.secrets.webhookSecret` (mismatch
returns 401).

### Security recommendation

Use a webhook secret distinct from the admin API key (set both to
independently-generated random strings of at least 32 hex chars). The
combo "Ingress + custom-header X-Api-Key + NetworkPolicy ingress
allow-list" is defense-in-depth. Empty `webhookSecret` is acceptable
only when NetworkPolicy restricts ingress to a single trusted
namespace; do NOT combine empty `webhookSecret` with a public-Internet
Ingress.

## Scope

- ✅ Regular TV series.
- ❌ Anime (absolute numbering, batch release semantics) — not supported.

## License

[GPL-3.0](./LICENSE)
