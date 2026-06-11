#!/usr/bin/env node
// Story 121c §F — flatten en.ts vs ru.ts, report key-set asymmetry
// and missing CLDR plural buckets in RU. Run from web/:
//   node scripts/i18n-audit.mjs
//
// Exit code 0 = clean. Exit 1 = report has findings.

import { readFile, writeFile, mkdtemp, rm } from 'node:fs/promises';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = join(__dirname, '..');

// The locale files are valid JS modules once we strip the TypeScript
// `export type` tails and a couple of TS-only tokens. Each file is
// just one object literal `export const NAME = { ... } as const?;`
// We strip `as const`, drop any `export type` line, and write to a
// temp `.mjs` so Node can dynamic-import it natively. This avoids
// the brittle quote-replacement that broke on contractions like
// `"hasn't"`.
async function loadLocale(relPath, exportName) {
  const txt = await readFile(join(webRoot, relPath), 'utf8');
  const stripped = txt
    .replace(/^\s*import\s+type\s[\s\S]*?;\s*$/gm, '')
    .replace(/\bas\s+const\b/g, '')
    .replace(/^\s*export\s+type\s[\s\S]*?;\s*$/gm, '')
    // Strip `: TypeName` annotation on the `export const NAME` line.
    .replace(/(export\s+const\s+\w+)\s*:\s*[A-Za-z_$][\w$]*\s*=/, '$1 =');
  const tmpDir = await mkdtemp(join(tmpdir(), 'i18n-audit-'));
  const tmpFile = join(tmpDir, 'locale.mjs');
  await writeFile(tmpFile, stripped, 'utf8');
  try {
    const mod = await import(pathToFileURL(tmpFile).href);
    return mod[exportName];
  } finally {
    await rm(tmpDir, { recursive: true, force: true });
  }
}

function flatten(obj, prefix = '') {
  const out = new Set();
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      for (const sub of flatten(v, key)) out.add(sub);
    } else {
      out.add(key);
    }
  }
  return out;
}

const en = await loadLocale('src/i18n/locales/en.ts', 'en');
const ru = await loadLocale('src/i18n/locales/ru.ts', 'ru');

const enKeys = flatten(en);
const ruKeys = flatten(ru);

// Plural-suffix keys (_one/_few/_many/_other/_zero/_two) are excluded
// from the cross-language diff because CLDR plural buckets legitimately
// differ between languages (EN: one/other; RU: one/few/many/other).
// Their completeness is enforced by the per-language plural check below.
const PLURAL_SUFFIX_RE = /_(?:one|few|many|other|zero|two)$/;
const missingInRu = [...enKeys]
  .filter((k) => !ruKeys.has(k) && !PLURAL_SUFFIX_RE.test(k))
  .sort();
const missingInEn = [...ruKeys]
  .filter((k) => !enKeys.has(k) && !PLURAL_SUFFIX_RE.test(k))
  .sort();

// Russian CLDR plurals require _one, _few, _many, _other. Find keys
// in RU that have _one but are missing _many.
const ruPluralFamilies = new Map(); // base → set(suffix)
for (const k of ruKeys) {
  const m = k.match(/^(.+?)_(one|few|many|other|zero|two)$/);
  if (m) {
    const set = ruPluralFamilies.get(m[1]) ?? new Set();
    set.add(m[2]);
    ruPluralFamilies.set(m[1], set);
  }
}
const incompletePlurals = [];
for (const [base, suffixes] of ruPluralFamilies) {
  if (suffixes.has('one') && !suffixes.has('many')) {
    incompletePlurals.push(base);
  }
}

let bad = 0;
if (missingInRu.length) {
  console.log(`Missing in RU (${missingInRu.length}):`);
  for (const k of missingInRu) console.log(`  ${k}`);
  bad = 1;
}
if (missingInEn.length) {
  console.log(`\nMissing in EN (${missingInEn.length}):`);
  for (const k of missingInEn) console.log(`  ${k}`);
  bad = 1;
}
if (incompletePlurals.length) {
  console.log(`\nRU plural families missing _many (${incompletePlurals.length}):`);
  for (const k of incompletePlurals.sort()) console.log(`  ${k}`);
  bad = 1;
}
if (!bad) console.log('i18n audit: clean');
process.exit(bad);
