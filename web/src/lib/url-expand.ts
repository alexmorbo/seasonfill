// URL grammar for ScanDetail's "expanded" param (Q-011d-3). Comma-
// separated, per-element `encodeURIComponent`. We splice directly into
// the query string (NOT via `URLSearchParams.set`, which would re-
// encode `%` → `%25` and use `+` for spaces). Empty set still writes
// `expanded=` — key presence is the "user interacted" signal (r1).

const PARAM = 'expanded';

export function readExpanded(search: string): Set<string> {
  const raw = new URLSearchParams(search).get(PARAM);
  if (!raw) return new Set<string>();
  const out = new Set<string>();
  for (const member of raw.split(',')) {
    if (!member) continue;
    try { out.add(decodeURIComponent(member)); } catch { /* skip bad escape */ }
  }
  return out;
}

export function writeExpanded(search: string, expanded: ReadonlySet<string>): string {
  const sp = new URLSearchParams(search);
  sp.delete(PARAM);
  const rest = sp.toString();
  const value = [...expanded].map((m) => encodeURIComponent(m)).join(',');
  const ours = `${PARAM}=${value}`;
  return rest ? `${rest}&${ours}` : ours;
}
