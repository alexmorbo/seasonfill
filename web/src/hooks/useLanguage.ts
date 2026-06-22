import { useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ApiError, api } from '@/lib/api';
import type { MeResponse } from '@/lib/me-types';
import { ME_QUERY_KEY } from './useMe';
import { SUPPORTED_LANGS } from '@/i18n';

// Persisted localStorage key — keep aligned with i18n/index.ts
// LanguageDetector (`lookupLocalStorage: 'seasonfill.lang'`). Writing
// here lets unauth flows (e.g. /login page) pick up the user's last
// chosen language even before /api/v1/me resolves.
const LANG_LOCAL_STORAGE_KEY = 'seasonfill.lang';

export interface UseLanguageHandle {
  /** Current language code — preferred_language from /me, falls back
   *  to i18n.resolvedLanguage, then 'en'. */
  readonly current: string;
  /** Dual-write language change: optimistic cache update + i18n
   *  switch + localStorage + PATCH. Rollback + toast on PATCH error. */
  readonly setLanguage: (code: string) => Promise<void>;
}

// useLanguage centralises the dual-write language change flow for
// N-6 + N-7c. Three call sites need it: header <LanguageSwitcher>,
// <AppearanceSection> language radio, and any future Profile re-edit.
// Keeping the write sequence inside one hook prevents drift.
//
// Write order:
//   1. Optimistically update React Query cache (so other consumers
//      that read useMe() see the new value immediately).
//   2. Call i18n.changeLanguage(code) — synchronous resolve from
//      bundled resources, no network.
//   3. Write localStorage.seasonfill.lang for unauth flow detection.
//   4. PATCH /api/v1/me/settings with {preferred_language: code}.
//
// On PATCH failure: rollback the optimistic cache to its previous
// value AND revert i18n + localStorage. Surface a sonner error toast.
// (Reverting i18n keeps the user's display in sync with the server
// truth — they see the failure visually, not just as a toast.)
export function useLanguage(): UseLanguageHandle {
  const qc = useQueryClient();
  const { i18n, t } = useTranslation();
  const cached = qc.getQueryData<MeResponse>(ME_QUERY_KEY);
  const current =
    cached?.preferred_language ??
    i18n.resolvedLanguage ??
    'en';

  const setLanguage = useCallback(
    async (code: string) => {
      if (!(SUPPORTED_LANGS as readonly string[]).includes(code)) {
        return;
      }
      const previous = qc.getQueryData<MeResponse>(ME_QUERY_KEY);
      const previousI18nLang = i18n.resolvedLanguage ?? 'en';

      // Optimistic apply
      if (previous) {
        qc.setQueryData<MeResponse>(ME_QUERY_KEY, {
          ...previous,
          preferred_language: code,
        });
      }
      await i18n.changeLanguage(code);
      try {
        window.localStorage.setItem(LANG_LOCAL_STORAGE_KEY, code);
      } catch {
        // Storage may be unavailable (private mode, quota). Skip
        // silently — the PATCH is the source of truth anyway.
      }

      // Persist server-side. On error revert everything.
      try {
        await api<MeResponse>('/me/settings', {
          method: 'PATCH',
          body: { preferred_language: code },
        });
      } catch (err) {
        if (previous) {
          qc.setQueryData<MeResponse>(ME_QUERY_KEY, previous);
        }
        await i18n.changeLanguage(previousI18nLang);
        try {
          window.localStorage.setItem(LANG_LOCAL_STORAGE_KEY, previousI18nLang);
        } catch {
          // ignore
        }
        const msg = err instanceof ApiError ? err.message : String(err);
        toast.error(t('settings.profile.language_save_failed', { msg }));
      }
    },
    [qc, i18n, t],
  );

  return { current, setLanguage };
}
