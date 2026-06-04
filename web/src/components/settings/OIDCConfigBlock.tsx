import { useTranslation } from 'react-i18next';
import { useState } from 'react';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Info, AlertTriangle } from 'lucide-react';

// OIDCField helpers — kept as a list-of-strings chip editor for scopes and
// allowed_groups, identical UX to TrustedProxiesEditor / LocalNetworksEditor
// but without the CIDR validation (these are plain strings).
function ChipList(props: {
  id: string;
  value: string[];
  onChange: (next: string[]) => void;
  placeholder: string;
  addAria: string;
  removeAria: (entry: string) => string;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-1.5">
        {props.value.map((entry) => (
          <span
            key={entry}
            className="inline-flex items-center gap-1 px-2 py-0.5 text-[12px] rounded bg-surface-2 border border-border"
          >
            {entry}
            <button
              type="button"
              aria-label={props.removeAria(entry)}
              className="text-muted hover:text-foreground"
              onClick={() => props.onChange(props.value.filter((e) => e !== entry))}
            >
              ×
            </button>
          </span>
        ))}
      </div>
      <Input
        id={props.id}
        type="text"
        placeholder={props.placeholder}
        aria-label={props.addAria}
        onKeyDown={(e) => {
          if (e.key !== 'Enter') return;
          e.preventDefault();
          const v = (e.currentTarget.value ?? '').trim();
          if (!v) return;
          if (props.value.includes(v)) return;
          props.onChange([...props.value, v]);
          e.currentTarget.value = '';
        }}
      />
    </div>
  );
}

// client_secret follows the dirty-bit pattern: undefined = no change
// (omit from PUT); string = send the value (empty string clears).
export type OIDCFormShape = {
  issuer: string;
  client_id: string;
  redirect_url: string;
  scopes: string[];
  username_claim: string;
  allowed_groups: string[];
  groups_claim: string;
  client_secret?: string;
  client_secret_configured: boolean;
  client_secret_env_override: boolean;
};

export function OIDCConfigBlock(props: {
  value: OIDCFormShape;
  onChange: (next: OIDCFormShape) => void;
  errors?: {
    issuer?: string;
    client_id?: string;
    redirect_url?: string;
    scopes?: string;
    client_secret?: string;
    groups_claim?: string;
  };
}) {
  const { t } = useTranslation();
  const v = props.value;
  const set = <K extends keyof OIDCFormShape>(k: K, val: OIDCFormShape[K]) =>
    props.onChange({ ...v, [k]: val });
  const [secretFocused, setSecretFocused] = useState(false);
  const secretDirty = v.client_secret !== undefined;
  const secretInputValue = secretDirty
    ? (v.client_secret ?? '')
    : v.client_secret_configured && !secretFocused
      ? '••••••••'
      : '';

  return (
    <div className="flex flex-col gap-4">
      <h4 className="text-[13px] font-semibold tracking-tight">
        {t('settings.security.oidc.section')}
      </h4>

      {v.client_secret_env_override && (
        <Alert>
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('settings.security.oidc.clientSecret.envOverrideTitle')}</AlertTitle>
          <AlertDescription>
            {t('settings.security.oidc.clientSecret.envOverrideBody')}
          </AlertDescription>
        </Alert>
      )}

      <div className="flex flex-col gap-1.5 max-w-md">
        <Label htmlFor="oidc-issuer">{t('settings.security.oidc.issuer.label')}</Label>
        <Input
          id="oidc-issuer"
          type="url"
          placeholder="https://keycloak.example.com/realms/homelab"
          value={v.issuer}
          onChange={(e) => set('issuer', e.target.value)}
          aria-invalid={Boolean(props.errors?.issuer) || undefined}
        />
        <p className="text-[11.5px] text-muted">
          {t('settings.security.oidc.issuer.hint')}
        </p>
        {props.errors?.issuer && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(props.errors.issuer)}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5 max-w-md">
        <Label htmlFor="oidc-client-id">{t('settings.security.oidc.clientId.label')}</Label>
        <Input
          id="oidc-client-id"
          type="text"
          value={v.client_id}
          onChange={(e) => set('client_id', e.target.value)}
          aria-invalid={Boolean(props.errors?.client_id) || undefined}
        />
        {props.errors?.client_id && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(props.errors.client_id)}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5 max-w-md">
        <Label htmlFor="oidc-client-secret">
          {t('settings.security.oidc.clientSecret.label')}
        </Label>
        <Input
          id="oidc-client-secret"
          type={secretFocused || secretDirty ? 'text' : 'password'}
          autoComplete="new-password"
          value={secretInputValue}
          placeholder={t('settings.security.oidc.clientSecret.placeholder')}
          onFocus={() => {
            setSecretFocused(true);
            // First focus on a configured-but-not-yet-typed field clears the
            // mask so the user types into an empty input.
            if (!secretDirty && v.client_secret_configured) {
              set('client_secret', '');
            }
          }}
          onBlur={() => setSecretFocused(false)}
          onChange={(e) => set('client_secret', e.target.value)}
          aria-invalid={Boolean(props.errors?.client_secret) || undefined}
          disabled={v.client_secret_env_override}
        />
        <p className="text-[11.5px] text-muted">
          {v.client_secret_configured
            ? t('settings.security.oidc.clientSecret.hintConfigured')
            : t('settings.security.oidc.clientSecret.hintEmpty')}
        </p>
        {props.errors?.client_secret && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(props.errors.client_secret)}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5 max-w-md">
        <Label htmlFor="oidc-redirect">{t('settings.security.oidc.redirectUrl.label')}</Label>
        <Input
          id="oidc-redirect"
          type="url"
          placeholder="https://seasonfill.example.com/api/v1/auth/oidc/callback"
          value={v.redirect_url}
          onChange={(e) => set('redirect_url', e.target.value)}
          aria-invalid={Boolean(props.errors?.redirect_url) || undefined}
        />
        <p className="text-[11.5px] text-muted">
          {t('settings.security.oidc.redirectUrl.hint')}
        </p>
        {props.errors?.redirect_url && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(props.errors.redirect_url)}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="oidc-scopes">{t('settings.security.oidc.scopes.label')}</Label>
        <ChipList
          id="oidc-scopes"
          value={v.scopes}
          onChange={(next) => set('scopes', next)}
          placeholder="openid"
          addAria={t('settings.security.oidc.scopes.addAria')}
          removeAria={(e) => t('settings.security.oidc.scopes.removeAria', { scope: e })}
        />
        <p className="text-[11.5px] text-muted">
          {t('settings.security.oidc.scopes.hint')}
        </p>
        {props.errors?.scopes && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(props.errors.scopes)}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5 max-w-md">
        <Label htmlFor="oidc-username-claim">
          {t('settings.security.oidc.usernameClaim.label')}
        </Label>
        <Input
          id="oidc-username-claim"
          type="text"
          value={v.username_claim}
          onChange={(e) => set('username_claim', e.target.value)}
        />
        <p className="text-[11.5px] text-muted">
          {t('settings.security.oidc.usernameClaim.hint')}
        </p>
      </div>

      <div className="flex flex-col gap-1.5 max-w-md">
        <Label htmlFor="oidc-groups-claim">
          {t('settings.security.oidc.groupsClaim.label')}
        </Label>
        <Input
          id="oidc-groups-claim"
          type="text"
          value={v.groups_claim}
          placeholder="groups"
          onChange={(e) => set('groups_claim', e.target.value)}
          aria-invalid={Boolean(props.errors?.groups_claim) || undefined}
        />
        <p className="text-[11.5px] text-muted">
          {t('settings.security.oidc.groupsClaim.hint')}
        </p>
        {props.errors?.groups_claim && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(props.errors.groups_claim)}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="oidc-groups">{t('settings.security.oidc.allowedGroups.label')}</Label>
        <ChipList
          id="oidc-groups"
          value={v.allowed_groups}
          onChange={(next) => set('allowed_groups', next)}
          placeholder="admins"
          addAria={t('settings.security.oidc.allowedGroups.addAria')}
          removeAria={(e) => t('settings.security.oidc.allowedGroups.removeAria', { group: e })}
        />
        <p className="text-[11.5px] text-muted">
          {t('settings.security.oidc.allowedGroups.hint')}
        </p>
      </div>

      <Alert>
        <Info className="w-4 h-4" />
        <AlertDescription>{t('settings.security.oidc.envHint')}</AlertDescription>
      </Alert>
    </div>
  );
}
