// locale.ts maps the FE's short i18n codes ("en" / "ru") to the BCP-47
// tags the BE picker keys exactly ("en-US" / "ru-RU"). Story 540 /
// B-49: passing `lang=ru` to /discovery/genres returns en-US fallbacks
// because genres_i18n.language stores "ru-RU".
//
// Returns undefined when the input is undefined/empty so callers can
// keep their `lang ? { lang } : {}` spread pattern. Already-tagged
// input ("en-US") passes through unchanged.

const SHORT_TO_BCP47: Readonly<Record<string, string>> = {
  en: 'en-US',
  ru: 'ru-RU',
};

export function toBcp47(short?: string | null): string | undefined {
  if (!short) return undefined;
  if (short.includes('-')) return short;
  return SHORT_TO_BCP47[short] ?? short;
}
