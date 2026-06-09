import { Loader2, PlugZap } from 'lucide-react';
import type { Control, FieldErrors, UseFormRegister } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { KeyRound } from 'lucide-react';
import { WebhookSubCard } from './WebhookSubCard';

export interface ConnectionSectionProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<any, any, any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly register: UseFormRegister<any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly errors: FieldErrors<any>;
  readonly mode: 'create' | 'edit';
  readonly instanceName: string | undefined;
  readonly installEnabled: boolean;
  readonly uiUrlHint: string | undefined;
  readonly onTest: () => void;
  readonly testing: boolean;
  readonly probeResult: string | null;
  readonly tValidationError: (msg: string | undefined) => string;
}

// NOTE: Mode + Dry-run are NOT here — they live in <PromotedControls>
// at the top of the dialog body, per design HTML (`.promo` block).
export function ConnectionSection({
  control, register, errors, mode, instanceName, installEnabled,
  uiUrlHint, onTest, testing, probeResult, tValidationError,
}: ConnectionSectionProps) {
  const { t } = useTranslation();
  const isEdit = mode === 'edit';
  return (
    <div className="flex flex-col gap-4" data-testid="connection-section">
      {/*
        Operator R4: align the four inputs horizontally by laying out
        each row pair (Name+URL, then Public-URL+API-key) on a CSS grid
        with explicit row-template `auto auto auto` — label band, input
        band, help band. Cells explicitly claim their row via
        row-start-{1,2,3} so labels of different heights (the API-key
        label includes the "Шифруется на диске" badge that's 3.5px
        taller than the bare Public URL label) all share row 1 and
        auto-equalize via the row track, dragging the inputs into
        perfect alignment on row 2.
      */}
      <div className="grid grid-cols-2 grid-rows-[auto_auto_auto] gap-x-4 gap-y-1.5 items-start">
        {/* Row 1 / Col 1 — Name label */}
        <div className="row-start-1 col-start-1 flex items-center">
          <Label htmlFor="inst-name">{t('settings.instances.form.nameLabel')}</Label>
        </div>
        {/* Row 1 / Col 2 — URL label */}
        <div className="row-start-1 col-start-2 flex items-center">
          <Label htmlFor="inst-url">{t('settings.instances.form.urlLabel')}</Label>
        </div>
        {/* Row 2 / Col 1 — Name input */}
        <div className="row-start-2 col-start-1">
          <Input
            id="inst-name"
            autoFocus={!isEdit}
            disabled={isEdit}
            className="font-mono"
            placeholder={t('settings.instances.form.connection.namePlaceholder')}
            aria-invalid={Boolean(errors.name) || undefined}
            {...register('name')}
          />
        </div>
        {/* Row 2 / Col 2 — URL input */}
        <div className="row-start-2 col-start-2">
          <Input
            id="inst-url"
            type="url"
            className="font-mono"
            placeholder={t('settings.instances.form.connection.urlPlaceholder')}
            aria-invalid={Boolean(errors.url) || undefined}
            {...register('url')}
          />
        </div>
        {/* Row 3 / Col 1 — Name help/errors */}
        <div className="row-start-3 col-start-1 flex flex-col gap-1">
          {isEdit && (
            <p className="text-[11.5px] text-tx-muted">
              {t('settings.instances.form.nameImmutableHint')}
            </p>
          )}
          {errors.name && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t((errors.name.message as string) ?? '', { defaultValue: (errors.name.message as string) ?? '' })}
            </p>
          )}
        </div>
        {/* Row 3 / Col 2 — URL errors */}
        <div className="row-start-3 col-start-2 flex flex-col gap-1">
          {errors.url && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t((errors.url.message as string) ?? '', { defaultValue: (errors.url.message as string) ?? '' })}
            </p>
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 grid-rows-[auto_auto_auto] gap-x-4 gap-y-1.5 items-start">
        {/* Row 1 / Col 1 — Public URL label */}
        <div className="row-start-1 col-start-1 flex items-center">
          <Label htmlFor="inst-public-url">
            {t('settings.instances.form.publicUrlLabel')}
          </Label>
        </div>
        {/* Row 1 / Col 2 — API key label + badge */}
        <div className="row-start-1 col-start-2 flex items-center gap-2">
          <Label htmlFor="inst-key">{t('settings.instances.form.apiKeyLabel')}</Label>
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="secondary" className="gap-1 text-[10.5px]">
                  <KeyRound className="w-3 h-3" />
                  {t('settings.instances.form.apiKeyEncrypted')}
                </Badge>
              </TooltipTrigger>
              <TooltipContent>
                {t('settings.instances.form.apiKeyEncryptedTooltip')}
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
        {/* Row 2 / Col 1 — Public URL input */}
        <div className="row-start-2 col-start-1">
          <Input
            id="inst-public-url"
            type="url"
            className="font-mono"
            placeholder={t('settings.instances.form.publicUrlPlaceholder')}
            aria-invalid={Boolean(errors.public_url) || undefined}
            {...register('public_url')}
          />
        </div>
        {/* Row 2 / Col 2 — API key input */}
        <div className="row-start-2 col-start-2">
          <Input
            id="inst-key"
            type="password"
            autoComplete="off"
            className="font-mono"
            placeholder={
              isEdit
                ? t('settings.instances.form.apiKeyKeepPlaceholder')
                : t('settings.instances.form.connection.apiKeyPlaceholder')
            }
            aria-invalid={Boolean(errors.api_key) || undefined}
            {...register('api_key')}
          />
        </div>
        {/* Row 3 / Col 1 — Public URL help + errors */}
        <div className="row-start-3 col-start-1 flex flex-col gap-1">
          <p className="text-[11.5px] text-tx-muted max-w-prose">
            {t('settings.instances.form.publicUrlHelp')}
          </p>
          {uiUrlHint && (
            <p className="text-[11.5px] text-tx-muted" data-testid="inst-ui-url-hint">
              {t('settings.instances.form.uiUrlHint', { url: uiUrlHint })}
            </p>
          )}
          {errors.public_url && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t((errors.public_url.message as string) ?? '', {
                defaultValue: (errors.public_url.message as string) ?? '',
              })}
            </p>
          )}
        </div>
        {/* Row 3 / Col 2 — API key help + errors */}
        <div className="row-start-3 col-start-2 flex flex-col gap-1">
          {isEdit && (
            <p className="text-[11.5px] text-tx-muted">
              {t('settings.instances.form.apiKeyKeepHint')}
            </p>
          )}
          {errors.api_key && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t((errors.api_key.message as string) ?? '', {
                defaultValue: (errors.api_key.message as string) ?? '',
              })}
            </p>
          )}
        </div>
      </div>

      {!isEdit && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="self-start gap-1.5"
          onClick={onTest}
          disabled={testing}
          data-testid="inst-test-button"
        >
          {testing
            ? <Loader2 className="w-3.5 h-3.5 animate-spin" />
            : <PlugZap className="w-3.5 h-3.5" />}
          {t('settings.instances.form.testConnection')}
        </Button>
      )}
      {probeResult && (
        <p role="status" className="text-[12px] text-foreground-2">
          {probeResult}
        </p>
      )}

      {/* Webhook sub-card */}
      <WebhookSubCard
        control={control}
        mode={mode}
        instanceName={instanceName}
        installEnabled={installEnabled}
        register={register}
        errorOverride={tValidationError((errors.webhook_url_override?.message as string) ?? undefined)}
      />
    </div>
  );
}
