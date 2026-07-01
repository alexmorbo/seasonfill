import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { en } from './locales/en';
import { ru } from './locales/ru';

// Canonical BCP-47 codes matching server contract. `series_texts.language`
// stores 'en-US'/'ru-RU' exactly; genres_i18n / networks_i18n same.
// Aligning FE keys к server canon removes the entire toBcp47() shim class
// of bugs — the wire code equals the i18n code equals the /me code.
//
// Story 564 (B-lang). Previous config used ['en','ru'] + nonExplicitSupportedLngs
// which silently downgraded 'ru-RU' к 'ru' → wire mismatch → EN fallback.
export const SUPPORTED_LANGS = ['en-US', 'ru-RU'] as const;
export type Lang = (typeof SUPPORTED_LANGS)[number];

const LANG_LOCAL_STORAGE_KEY = 'seasonfill.lang';

// migrateStoredLang normalizes any legacy or family-only value in
// localStorage к a canonical BCP-47 code. Runs eagerly before i18n.init()
// so LanguageDetector reads the already-fixed value on first render.
//
// Migration table:
//   'en'      → 'en-US'
//   'ru'      → 'ru-RU'
//   'en-US'   → 'en-US' (pass-through)
//   'ru-RU'   → 'ru-RU' (pass-through)
//   'en-GB'   → 'en-US' (family match, canonical variant)
//   'ru-BY'   → 'ru-RU' (family match, canonical variant)
//   anything else → cleared (falls back к navigator/fallbackLng)
//
// Exported для reuse in tests + potential future NavigatorLanguage flow.
export function normalizeLangCode(input: string | null | undefined): Lang | null {
  if (!input) return null;
  const lower = input.toLowerCase();
  if (lower === 'en-us') return 'en-US';
  if (lower === 'ru-ru') return 'ru-RU';
  const dash = lower.indexOf('-');
  const family = dash === -1 ? lower : lower.slice(0, dash);
  if (family === 'en') return 'en-US';
  if (family === 'ru') return 'ru-RU';
  return null;
}

// Eager one-shot migration: read → normalize → write back (or clear).
// Wrapped в try/catch because localStorage может быть недоступен (private
// mode, quota, SSR-like environments в тестах). Failure is silent —
// LanguageDetector will fall back к navigator → fallbackLng.
function migrateLocalStorageLang(): void {
  try {
    const raw = window.localStorage.getItem(LANG_LOCAL_STORAGE_KEY);
    if (raw === null) return;
    const normalized = normalizeLangCode(raw);
    if (normalized === null) {
      window.localStorage.removeItem(LANG_LOCAL_STORAGE_KEY);
      return;
    }
    if (normalized !== raw) {
      window.localStorage.setItem(LANG_LOCAL_STORAGE_KEY, normalized);
    }
  } catch {
    // localStorage unavailable — skip silently.
  }
}

migrateLocalStorageLang();

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      'en-US': { translation: en },
      'ru-RU': { translation: ru },
    },
    fallbackLng: 'en-US',
    supportedLngs: SUPPORTED_LANGS as unknown as string[],
    // nonExplicitSupportedLngs removed intentionally: previously it
    // silently normalized 'ru-RU' → 'ru' when 'ru' was in supportedLngs,
    // which broke wire compatibility with BE series_texts.language. Now
    // we only accept exact BCP-47 codes; navigator variants ('en-GB',
    // 'ru-BY') are handled by migrateLocalStorageLang + explicit
    // normalization on the detection path.
    interpolation: { escapeValue: false },
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: LANG_LOCAL_STORAGE_KEY,
      caches: ['localStorage'],
      // Convert detected values through normalizeLangCode so that
      // navigator.language = 'en-GB' resolves к 'en-US' instead of
      // triggering fallbackLng.
      convertDetectedLanguage: (lng: string): string => {
        return normalizeLangCode(lng) ?? 'en-US';
      },
    },
    returnNull: false,
  });

export default i18n;
