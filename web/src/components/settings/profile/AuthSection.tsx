import { useTranslation } from 'react-i18next';
import { ExternalLink } from 'lucide-react';
import type { MeResponse } from '@/lib/me-types';
import { ChangePasswordForm } from './ChangePasswordForm';

export interface AuthSectionProps {
  readonly me: MeResponse;
}

// AuthSection is the password / IdP gate. Branches by the per-user auth_mode:
//   - 'forms' → mount <ChangePasswordForm>.
//   - 'oidc'  → render IdP profile link CTA (or disabled notice when
//               idp_profile_url is null).
//
// Story 487 (N-7c).
export function AuthSection({ me }: AuthSectionProps) {
  const { t } = useTranslation();

  return (
    <section
      data-testid="auth-section"
      data-auth-mode={me.auth_mode}
      className="flex flex-col gap-3.5"
    >
      <header className="flex flex-col gap-[3px]">
        <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0">
          {t('settings.profile.auth_section_title')}
        </h2>
      </header>

      {me.auth_mode === 'forms' && <ChangePasswordForm />}

      {me.auth_mode === 'oidc' && (
        <div className="py-[11px]">
          {me.idp_profile_url !== null ? (
            <a
              href={me.idp_profile_url}
              target="_blank"
              rel="noopener noreferrer"
              data-testid="oidc-profile-link"
              className="inline-flex items-center gap-1.5 text-[13.5px] text-accent underline hover:no-underline"
            >
              {t('settings.profile.oidc_profile_link')}
              <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
            </a>
          ) : (
            <p
              data-testid="oidc-no-profile-url"
              className="text-[13px] text-muted m-0"
            >
              {t('settings.profile.oidc_managed_no_link')}
            </p>
          )}
        </div>
      )}
    </section>
  );
}
