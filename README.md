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

## Scope

- ✅ Regular TV series.
- ❌ Anime (absolute numbering, batch release semantics) — not supported.

## License

[GPL-3.0](./LICENSE)
