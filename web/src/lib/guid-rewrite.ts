// Tracker GUID rewrites — operator-curated substring substitutions applied
// client-side before rendering "open on tracker" links. The persisted GUIDs
// in grab_records / origin_releases stay unchanged. Order matters: rules are
// applied left-to-right via repeated split/join (the JS equivalent of Go's
// strings.Replace(s, from, to, -1)). Story 108.

export interface GuidRewriteRule {
  readonly from: string;
  readonly to: string;
}

// applyGuidRewrites runs the operator rules over `guid` in array order.
// Empty `from` entries are skipped (the backend rejects them at PUT time,
// but a partially-typed rule mid-edit shouldn't break the preview).
export function applyGuidRewrites(
  guid: string,
  rules: readonly GuidRewriteRule[],
): string {
  let result = guid;
  for (const rule of rules) {
    if (!rule.from) continue;
    result = result.split(rule.from).join(rule.to);
  }
  return result;
}

// isTrackerUrl gates the "open on tracker" link. We only render it when the
// rewritten string is an http(s) URL — internal cluster URLs after rewrite
// (still starting with `http://`) are technically valid clickable links but
// the operator's intent for rewrites is "expose a public URL", so any
// non-http(s) leftover is treated as "no link".
export function isTrackerUrl(s: string): boolean {
  return /^https?:\/\//i.test(s);
}
