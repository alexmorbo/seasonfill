import { useTranslation } from 'react-i18next';

// ProfileTab is a placeholder for N-7c. The content (ProfileSection,
// AppearanceSection, AuthSection, Avatar, ChangePasswordForm) all land
// in story 487. This file exists so /settings/profile resolves to a
// real React node in N-7b and routing assertions can pass.
//
// Story 486 (N-7b).
export function ProfileTab() {
  const { t } = useTranslation();
  return (
    <div
      data-testid="profile-tab-placeholder"
      className="flex items-center justify-center min-h-[160px] rounded-md border border-dashed border-border-faint bg-surface/40 text-tx-muted text-sm"
    >
      {t('settings.profile.placeholder')}
    </div>
  );
}
