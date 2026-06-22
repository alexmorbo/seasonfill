import { useTranslation } from 'react-i18next';
import { Loader2, AlertTriangle } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { useMe } from '@/hooks/useMe';
import { ProfileSection } from '@/components/settings/profile/ProfileSection';
import { AppearanceSection } from '@/components/settings/profile/AppearanceSection';
import { AuthSection } from '@/components/settings/profile/AuthSection';

// ProfileTab is the /settings/profile content. N-7b shipped this as a
// placeholder; N-7c fills it with three sibling sections fed by a
// shared useMe() snapshot. Loading + error states surface inline (the
// global api() wrapper handles 401 by redirecting).
//
// AuthSection self-hides for basic/none auth_mode (no user identity
// surface to manage). The other two sections render unconditionally.
//
// Story 487 (N-7c).
export function ProfileTab() {
  const { t } = useTranslation();
  const me = useMe();

  if (me.isLoading) {
    return (
      <div
        data-testid="profile-tab-loading"
        className="flex items-center gap-2 text-muted text-[13px]"
      >
        <Loader2 className="w-3.5 h-3.5 animate-spin" />
        {t('common.checkingSession')}
      </div>
    );
  }

  if (me.isError || !me.data) {
    return (
      <Alert variant="destructive" data-testid="profile-tab-error">
        <AlertTriangle className="w-4 h-4" />
        <AlertTitle>{t('settings.profile.load_failed_title')}</AlertTitle>
        <AlertDescription>{me.error?.message ?? ''}</AlertDescription>
      </Alert>
    );
  }

  return (
    <div data-testid="profile-tab" className="flex flex-col gap-7 max-w-[760px]">
      <ProfileSection me={me.data} />
      <AppearanceSection me={me.data} />
      <AuthSection me={me.data} />
    </div>
  );
}
