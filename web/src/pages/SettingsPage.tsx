import { useEffect } from 'react';
import { Outlet, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSetPageTitle } from '@/components/shell/page-title-context';

// Legacy hash → path routes. Operator-bookmarks live in the wild for
// /settings#general / #security / #integrations and the pre-N-7b
// /settings#instances → /instances jump from old Settings.tsx:32-36.
// Preserve all four for one ship-cycle; remove a future sprint once
// telemetry shows zero hits.
const HASH_REDIRECTS: Record<string, string> = {
  '#general': '/settings/system/general',
  '#security': '/settings/system/security',
  '#integrations': '/settings/system/integrations',
  '#instances': '/instances',
};

// SettingsPage is the routing shell for the Settings hub. It renders
// only the <Outlet /> — the tabbed UI lives in SystemLayout (system/*)
// and ProfileTab (profile). The shell exists so the legacy hash
// migration can run once per mount before any child route paints.
//
// Story 486 (N-7b).
export function SettingsPage() {
  const { t } = useTranslation();
  useSetPageTitle(t('settings.title'));
  const navigate = useNavigate();

  // One-shot hash migration. [] deps guarantees single execution per
  // mount; after the redirect, the hash is cleared via `replace`, so
  // re-firing would be a no-op anyway (the lookup table miss returns
  // undefined).
  useEffect(() => {
    if (typeof window === 'undefined') return;
    const target = HASH_REDIRECTS[window.location.hash];
    if (target !== undefined) {
      // Strip the hash from the URL the navigate() lands on so it
      // doesn't reappear after future client-side navigations.
      navigate(target, { replace: true });
    }
  }, [navigate]);

  return <Outlet />;
}
