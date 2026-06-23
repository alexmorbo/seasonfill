/// <reference types="node" />
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

const SRC_ROOT = resolve(__dirname, '..');

// Forbidden patterns: any per-instance URL for a globalized surface.
// This is the source-form regex (matches template-literal source
// AND string-concat source). The allowlist filters out per-instance
// URLs that are intentionally retained (queue, webhook, scan, etc.).
const FORBIDDEN =
  /\/instances\/[^/`'"\s$]+\/(?:series|series-cache|missing|counters|grabs\/[^/`'"\s$]+\/episode-files)\b/g;

// Allowlisted URLs — these stay per-instance per BE 492 + Risk §2.
const ALLOWED_SUBSTRINGS = [
  '/instances/${name}/queue',
  '/instances/${current}/queue',
  '/instances/${instance}/queue',
  '/instances/${instanceName}/queue',
  '/webhook/sonarr',
  '/qbit/settings',
  '/qbit/connect',
  '/discover/qbit',
  '/watchdog/blacklist',
  '/watchdog/rollups',
  '/watchdog/seasons',
];

// Files allowlisted entirely (e.g., the purge test itself, schema.ts
// which is generated and may carry old paths IF the BE has not yet
// trimmed stale @Router annotations, and legacy-emit detection tests
// that intentionally mention the strings in assertions).
const ALLOWED_FILES = new Set<string>([
  // schema.ts is generated; product-code coverage matters. Story N-1f
  // dropped the 11 catalog/seriesdetail @Router orphans from the BE,
  // but a single per-instance grabs/{id}/episode-files path remains
  // (tracked for the dead-code sweep follow-up) and the global series
  // DTOs still carry `@description` comments that mention the legacy
  // per-instance URLs by name. Keep the allowlist entry until both
  // are cleaned up by a future BE pass.
  'api/schema.ts',
  '__tests__/legacy-url-purge.test.ts',
  // Counters.ts intentionally references the old URL behind a
  // VITE_LEGACY_COUNTERS feature flag (Story 493 §C). 494 rewrites it.
  'lib/counters.ts',
  // Legacy-emit detection tests assert the absence of legacy URLs
  // and therefore mention them as strings in expectations.
  'components/series/SeriesPosterTile.test.tsx',
  'components/grabs/GrabRow.test.tsx',
  'components/dashboard/PosterTile.test.tsx',
]);

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      if (entry === 'node_modules' || entry.startsWith('.')) continue;
      out.push(...walk(full));
    } else if (st.isFile() && /\.(ts|tsx)$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

describe('legacy URL purge', () => {
  it('emits zero per-instance URLs for globalized surfaces', () => {
    const violations: { file: string; line: number; match: string }[] = [];
    const files = walk(SRC_ROOT);
    for (const file of files) {
      const rel = file.slice(SRC_ROOT.length + 1).replace(/\\/g, '/');
      if (ALLOWED_FILES.has(rel)) continue;
      const content = readFileSync(file, 'utf8');
      const lines = content.split('\n');
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i] ?? '';
        FORBIDDEN.lastIndex = 0;
        let m: RegExpExecArray | null;
        while ((m = FORBIDDEN.exec(line)) !== null) {
          const match = m[0];
          if (ALLOWED_SUBSTRINGS.some((a) => line.includes(a))) continue;
          violations.push({ file: rel, line: i + 1, match });
        }
      }
    }
    if (violations.length > 0) {
      const msg = violations
        .map((v) => `  ${v.file}:${v.line} — ${v.match}`)
        .join('\n');
      throw new Error(
        `Legacy URL purge gate failed (${violations.length} violation(s)):\n${msg}\n` +
        `If a URL must stay per-instance (queue, webhook, qbit, watchdog), add it to ALLOWED_SUBSTRINGS in this test.`,
      );
    }
    expect(violations).toEqual([]);
  });
});
