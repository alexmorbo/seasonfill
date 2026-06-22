import { Navigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useMe } from '@/hooks/useMe';

// SettingsRedirect is the index route of /settings. Reads the role
// off /api/v1/me and routes to the role-appropriate landing tab.
//
// Loading state: brief "checking session" message — matches the
// ProtectedRoute pattern at web/src/components/ProtectedRoute.tsx:8-9.
//
// Error state: the global api() wrapper already redirects on 401.
// Render the same checking-session message for the brief instant
// before the redirect resolves to avoid a janky "Error" flash.
//
// Story 486 (N-7b).
export function SettingsRedirect() {
  const { t } = useTranslation();
  const me = useMe();

  if (me.isLoading) {
    return (
      <div className="grid place-items-center h-32 text-faint mono">
        {t('common.checkingSession')}
      </div>
    );
  }

  if (me.data?.role === 'admin') {
    return <Navigate to="/settings/system/general" replace />;
  }

  // Non-admin OR error (briefly, before the global 401 redirect kicks
  // in). Either way, the right answer is to land on /profile.
  return <Navigate to="/settings/profile" replace />;
}
