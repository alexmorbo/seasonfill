import { useTranslation } from 'react-i18next';
import { formatDate } from '@/lib/timezone';
import { Avatar } from '@/components/Avatar';
import type { MeResponse } from '@/lib/me-types';

export interface ProfileSectionProps {
  readonly me: MeResponse;
}

// ProfileSection is a read-only identity card. Renders username,
// email, role, last_login_at + a 64px Avatar. The card layout
// mirrors the settings cadence (no <Card> wrapper, just a <section>
// with header + flex body) so it visually flows with the existing
// General/Security/Integrations tabs.
//
// Avatar mode comes from me.avatar_resolved_mode (BE-resolved) — the
// stored me.avatar_mode preference shows up in AppearanceSection's
// radio, not here.
//
// Story 487 (N-7c).
export function ProfileSection({ me }: ProfileSectionProps) {
  const { t } = useTranslation();
  const roleLabel =
    me.role === 'admin'
      ? t('settings.profile.role_admin')
      : t('settings.profile.role_user');
  const emailLabel = me.email ?? '—';
  const lastLogin = me.last_login_at
    ? formatDate(me.last_login_at, 'datetime')
    : '—';

  return (
    <section
      data-testid="profile-section"
      className="flex flex-col gap-3.5"
    >
      <header className="flex flex-col gap-[3px]">
        <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0">
          {t('settings.profile.section_title')}
        </h2>
      </header>
      <div className="flex gap-5 items-start">
        <Avatar
          avatar_resolved_mode={me.avatar_resolved_mode}
          avatar_hash={me.avatar_hash}
          username={me.username}
          size={64}
          data-testid="profile-section-avatar"
        />
        <dl className="grid grid-cols-[max-content_1fr] gap-x-5 gap-y-2 flex-1">
          <dt className="text-[12.5px] text-muted">
            {t('settings.profile.username')}
          </dt>
          <dd data-testid="profile-username" className="text-[13.5px] font-[550] m-0">
            {me.username}
          </dd>
          <dt className="text-[12.5px] text-muted">
            {t('settings.profile.email')}
          </dt>
          <dd data-testid="profile-email" className="text-[13.5px] m-0">
            {emailLabel}
          </dd>
          <dt className="text-[12.5px] text-muted">
            {t('settings.profile.role')}
          </dt>
          <dd data-testid="profile-role" className="text-[13.5px] m-0">
            {roleLabel}
          </dd>
          <dt className="text-[12.5px] text-muted">
            {t('settings.profile.last_login')}
          </dt>
          <dd data-testid="profile-last-login" className="text-[13.5px] m-0">
            {lastLogin}
          </dd>
        </dl>
      </div>
    </section>
  );
}
