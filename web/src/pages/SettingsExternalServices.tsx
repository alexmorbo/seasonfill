import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { CheckCircle2, AlertTriangle, Loader2, Globe } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { ApiError } from '@/lib/api';
import {
  type ExternalServiceDTO,
  type ExternalServiceName,
  type ExternalServiceOutcome,
  type ExternalServiceUpsertRequest,
  listExternalServices,
  testExternalService,
  upsertExternalService,
} from '@/api/externalServices';

const SERVICES: readonly ExternalServiceName[] = ['tmdb', 'omdb', 'tvdb'];

function relativeTime(iso?: string): string {
  if (!iso) return '';
  const then = new Date(iso).getTime();
  const now = Date.now();
  const diff = Math.max(0, now - then);
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

function OutcomePill({ outcome }: { outcome?: ExternalServiceOutcome }) {
  const { t } = useTranslation();
  if (!outcome) {
    return (
      <span
        data-testid="ext-status-pill"
        data-status="untested"
        className="inline-flex items-center gap-1.5 px-2.5 h-[20px] rounded-full font-mono text-[11.5px] font-semibold text-tx-faint bg-bg-surface-2"
      >
        {t('settings.externalServices.status.untested')}
      </span>
    );
  }
  if (outcome === 'ok') {
    return (
      <span
        data-testid="ext-status-pill"
        data-status="ok"
        className="inline-flex items-center gap-1.5 px-2.5 h-[20px] rounded-full font-mono text-[11.5px] font-semibold text-status-success bg-status-success-dim"
      >
        <CheckCircle2 className="w-3 h-3" aria-hidden="true" />
        {t('settings.externalServices.status.ok')}
      </span>
    );
  }
  return (
    <span
      data-testid="ext-status-pill"
      data-status={outcome}
      className="inline-flex items-center gap-1.5 px-2.5 h-[20px] rounded-full font-mono text-[11.5px] font-semibold text-status-danger bg-status-danger-dim"
    >
      <AlertTriangle className="w-3 h-3" aria-hidden="true" />
      {t(`settings.externalServices.status.${outcome}`)}
    </span>
  );
}

interface DraftState {
  enabled: boolean;
  apiKey: string;
  apiKeyDirty: boolean;
  proxyURL: string;
  proxyURLDirty: boolean;
  proxyUser: string;
  proxyUserDirty: boolean;
  proxyPass: string;
  proxyPassDirty: boolean;
}

function initialDraft(dto: ExternalServiceDTO): DraftState {
  return {
    enabled: dto.enabled,
    apiKey: '',
    apiKeyDirty: false,
    proxyURL: dto.proxy_host && dto.proxy_scheme ? `${dto.proxy_scheme}://${dto.proxy_host}` : '',
    proxyURLDirty: false,
    proxyUser: '',
    proxyUserDirty: false,
    proxyPass: '',
    proxyPassDirty: false,
  };
}

function ServiceCard({ dto }: { dto: ExternalServiceDTO }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [draft, setDraft] = useState<DraftState>(() => initialDraft(dto));
  // Story 489 (B-17): inline error rendered under the API Key input when
  // the BE rejects the key (422 external_service_invalid_key). Cleared
  // on the next save attempt and on successful save.
  const [inlineKeyError, setInlineKeyError] = useState<string | null>(null);

  const upsert = useMutation({
    mutationFn: async () => {
      const body: ExternalServiceUpsertRequest = { enabled: draft.enabled };
      if (draft.apiKeyDirty) body.api_key = draft.apiKey;
      if (draft.proxyURLDirty) body.proxy_url = draft.proxyURL;
      if (draft.proxyUserDirty) body.proxy_username = draft.proxyUser;
      if (draft.proxyPassDirty) body.proxy_password = draft.proxyPass;
      return upsertExternalService(dto.service, body);
    },
    onMutate: () => {
      setInlineKeyError(null);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['external-services'] });
      toast.success(t('settings.externalServices.savedOk', { service: dto.service.toUpperCase() }));
      setInlineKeyError(null);
      setDraft((d) => ({
        ...d,
        apiKey: '',
        apiKeyDirty: false,
        proxyUserDirty: false,
        proxyPassDirty: false,
        proxyURLDirty: false,
      }));
    },
    onError: (err: unknown) => {
      // Story 489 (B-17): 422 = upstream rejected the key. Surface the
      // error inline under the API Key input so the operator can fix it
      // without losing context. The form stays open.
      if (
        err instanceof ApiError &&
        err.status === 422 &&
        typeof err.body === 'object' &&
        err.body !== null &&
        'error' in err.body &&
        (err.body as { error?: unknown }).error === 'external_service_invalid_key'
      ) {
        setInlineKeyError(t('settings.externalServices.invalidKey.saveError'));
        qc.invalidateQueries({ queryKey: ['external-services'] });
        return;
      }
      toast.error(
        t('settings.externalServices.savedErr', {
          service: dto.service.toUpperCase(),
          err: err instanceof Error ? err.message : String(err),
        }),
      );
    },
  });

  const test = useMutation({
    mutationFn: async () => testExternalService(dto.service),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['external-services'] });
      if (res.outcome === 'ok') {
        toast.success(
          t('settings.externalServices.testOk', {
            service: dto.service.toUpperCase(),
            ms: res.latency_ms,
          }),
        );
      } else {
        toast.error(
          t('settings.externalServices.testErr', {
            service: dto.service.toUpperCase(),
            outcome: res.outcome,
          }),
        );
      }
    },
    onError: (err: unknown) => {
      toast.error(
        t('settings.externalServices.testErr', {
          service: dto.service.toUpperCase(),
          outcome: err instanceof Error ? err.message : String(err),
        }),
      );
    },
  });

  const apiKeyPlaceholder = dto.api_key_configured
    ? dto.api_key_masked || '****'
    : t('settings.externalServices.apiKeyEmpty');

  return (
    <section
      data-testid={`ext-card-${dto.service}`}
      className="flex flex-col gap-4 p-4 rounded-md bg-bg-surface border border-border-faint"
    >
      <header className="flex items-center gap-3">
        <Globe className="w-4 h-4 text-tx-muted" aria-hidden="true" />
        <h3 className="text-[14px] font-[650] tracking-[-0.01em] m-0 uppercase">{dto.service}</h3>
        <div className="ml-auto flex items-center gap-2">
          {/* Story 489 (B-17): invalid-key badge sits alongside OutcomePill.
              Two distinct signals (Decision §7): OutcomePill = last manual
              POST /test result; validation badge = live 401 or rejected
              validate-on-save. */}
          {dto.last_validation_status === 'invalid_key' && (
            <span
              data-testid={`validation-badge-${dto.service}`}
              className="inline-flex items-center gap-1 px-2 h-[20px] rounded-full font-mono text-[11px] font-semibold text-status-danger bg-status-danger-dim"
            >
              <AlertTriangle className="w-3 h-3" aria-hidden="true" />
              {t('settings.externalServices.invalidKey.badge')}
            </span>
          )}
          {dto.last_test_outcome ? (
            <OutcomePill outcome={dto.last_test_outcome} />
          ) : (
            <OutcomePill />
          )}
          {dto.last_test_at && (
            <span className="text-[11.5px] text-tx-faint">
              {t('settings.externalServices.lastTest', { ago: relativeTime(dto.last_test_at) })}
            </span>
          )}
        </div>
      </header>

      <div className="flex items-center gap-2">
        <Switch
          checked={draft.enabled}
          onCheckedChange={(v) => setDraft((d) => ({ ...d, enabled: v }))}
          data-testid={`ext-enabled-${dto.service}`}
        />
        <Label className="text-[12.5px]">{t('settings.externalServices.enabled')}</Label>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label className="text-[12.5px] text-tx-secondary font-medium">
          {t('settings.externalServices.apiKey')}
        </Label>
        <Input
          type="password"
          placeholder={apiKeyPlaceholder}
          value={draft.apiKey}
          onChange={(e) => setDraft((d) => ({ ...d, apiKey: e.target.value, apiKeyDirty: true }))}
          data-testid={`ext-api-key-${dto.service}`}
        />
        {/* Story 489 (B-17): inline error rendered when validate-on-save
            returned 422 external_service_invalid_key. */}
        {inlineKeyError && (
          <p
            role="alert"
            data-testid={`ext-api-key-error-${dto.service}`}
            className="text-[11.5px] text-status-danger"
          >
            {inlineKeyError}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label className="text-[12.5px] text-tx-secondary font-medium">
          {t('settings.externalServices.proxyURL')}
        </Label>
        <Input
          type="text"
          placeholder="http(s)://… or socks5://…"
          value={draft.proxyURL}
          onChange={(e) => setDraft((d) => ({ ...d, proxyURL: e.target.value, proxyURLDirty: true }))}
          data-testid={`ext-proxy-url-${dto.service}`}
        />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1.5">
          <Label className="text-[12.5px] text-tx-secondary font-medium">
            {t('settings.externalServices.proxyUser')}
          </Label>
          <Input
            value={draft.proxyUser}
            onChange={(e) =>
              setDraft((d) => ({ ...d, proxyUser: e.target.value, proxyUserDirty: true }))
            }
            data-testid={`ext-proxy-user-${dto.service}`}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label className="text-[12.5px] text-tx-secondary font-medium">
            {t('settings.externalServices.proxyPass')}
          </Label>
          <Input
            type="password"
            value={draft.proxyPass}
            onChange={(e) =>
              setDraft((d) => ({ ...d, proxyPass: e.target.value, proxyPassDirty: true }))
            }
            data-testid={`ext-proxy-pass-${dto.service}`}
          />
        </div>
      </div>

      <div className="flex items-center gap-2 pt-1">
        <Button
          type="button"
          onClick={() => upsert.mutate()}
          disabled={upsert.isPending}
          data-testid={`ext-save-${dto.service}`}
        >
          {upsert.isPending && <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />}
          {t('settings.externalServices.save')}
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => test.mutate()}
          disabled={test.isPending || !dto.api_key_configured}
          data-testid={`ext-test-${dto.service}`}
        >
          {test.isPending && <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />}
          {t('settings.externalServices.test')}
        </Button>
      </div>
    </section>
  );
}

export function SettingsExternalServices() {
  const { t } = useTranslation();
  useSetPageTitle(t('settings.externalServices.title'));

  const q = useQuery({
    queryKey: ['external-services'],
    queryFn: listExternalServices,
  });

  if (q.isLoading) {
    return (
      <div className="flex flex-col gap-3" data-testid="ext-loading">
        {SERVICES.map((s) => (
          <Skeleton key={s} className="h-[260px] w-full rounded-md" />
        ))}
      </div>
    );
  }
  if (q.isError || !q.data) {
    return (
      <p className="text-[13px] text-status-danger" data-testid="ext-load-err">
        {t('settings.externalServices.loadErr')}
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-5 max-w-[760px]" data-testid="settings-external-services">
      <header>
        <h1 className="text-[18px] font-[650] tracking-[-0.01em] m-0">
          {t('settings.externalServices.title')}
        </h1>
        <p className="text-[13px] text-muted">{t('settings.externalServices.subtitle')}</p>
      </header>
      {q.data.map((dto) => (
        <ServiceCard key={dto.service} dto={dto} />
      ))}
    </div>
  );
}
