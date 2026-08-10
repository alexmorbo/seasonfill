import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Loader2, Send } from 'lucide-react';

import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Checkbox } from '@/components/ui/checkbox';
import { Badge } from '@/components/ui/badge';
import {
  DEFAULT_EVENT_TYPES,
  EVENT_TYPES,
  type NotificationAgentCreateRequest,
  type NotificationAgentUpdateRequest,
  type NotificationAgentView,
} from '@/api/notificationAgents';
import { useCreateAgent, useTestAgent, useUpdateAgent } from '@/lib/notification-agents-mutations';

const EVENT_TYPE_LABEL_KEY: Record<string, string> = {
  'grab.failed': 'settings.agents.eventTypes.grabFailed',
  'import.failed': 'settings.agents.eventTypes.importFailed',
  'grab.ok': 'settings.agents.eventTypes.grabOk',
  'watchdog.regrab': 'settings.agents.eventTypes.watchdogRegrab',
  'inbox.dead_letter': 'settings.agents.eventTypes.inboxDeadLetter',
  'season.premiere': 'settings.agents.eventTypes.seasonPremiere',
  'air_date.announced': 'settings.agents.eventTypes.airDateAnnounced',
  'digest.weekly': 'settings.agents.eventTypes.digestWeekly',
};

const nameRule = z.string().trim().min(1, 'settings.agents.errors.nameRequired').max(128);
const eventTypesRule = z.array(z.string());

// create vs edit (mirrors pickSchema in InstanceFormDialog): url is required
// on create, but on edit an empty url means "keep the existing config" so the
// field validates as an optional string.
const createSchema = z.object({
  name: nameRule,
  url: z.string().trim().min(1, 'settings.agents.errors.urlRequired'),
  enabled: z.boolean(),
  event_types: eventTypesRule,
});
const editSchema = z.object({
  name: nameRule,
  url: z.string(),
  enabled: z.boolean(),
  event_types: eventTypesRule,
});
type FormValues = z.infer<typeof createSchema>;
const pickSchema = (m: 'create' | 'edit') => (m === 'create' ? createSchema : editSchema);

export interface AgentFormDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (v: boolean) => void;
  readonly mode: 'create' | 'edit';
  readonly agent?: NotificationAgentView | undefined;
}

export function AgentFormDialog({ open, onOpenChange, mode, agent }: AgentFormDialogProps) {
  const { t } = useTranslation();
  const isEdit = mode === 'edit';
  const create = useCreateAgent();
  const update = useUpdateAgent();
  const test = useTestAgent();

  const {
    register, handleSubmit, reset, control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(pickSchema(mode)),
    defaultValues: {
      name: '',
      url: '',
      enabled: true,
      event_types: [...DEFAULT_EVENT_TYPES],
    },
  });

  // Seed the form on each open-transition. On edit the url field starts blank
  // (dirty-bit: an empty submit keeps the stored ciphertext); name/enabled/
  // event_types hydrate from the masked view.
  useEffect(() => {
    if (!open) return;
    if (isEdit && agent) {
      reset({
        name: agent.name ?? '',
        url: '',
        enabled: agent.enabled ?? false,
        event_types: agent.event_types ? [...agent.event_types] : [],
      });
    } else {
      reset({ name: '', url: '', enabled: true, event_types: [...DEFAULT_EVENT_TYPES] });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, isEdit, agent?.id]);

  const onSubmit = handleSubmit(async (values) => {
    if (isEdit && agent?.id != null) {
      const base: NotificationAgentUpdateRequest = {
        name: values.name.trim(),
        enabled: values.enabled,
        event_types: [...values.event_types],
      };
      const typedURL = values.url.trim();
      // Dirty-bit gate: only send `url` when the operator typed one. Empty =
      // keep the existing config (BE preserves the stored ciphertext).
      const body: NotificationAgentUpdateRequest =
        typedURL !== '' ? { ...base, url: typedURL } : base;
      await update.mutateAsync({ id: agent.id, body });
    } else {
      const body: NotificationAgentCreateRequest = {
        name: values.name.trim(),
        url: values.url.trim(),
        enabled: values.enabled,
        event_types: [...values.event_types],
      };
      await create.mutateAsync(body);
    }
    onOpenChange(false);
  });

  const title = isEdit
    ? t('settings.agents.form.editTitle')
    : t('settings.agents.form.createTitle');

  const canTest = isEdit && agent?.id != null && Boolean(agent?.configured);
  const testingThis = test.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[560px]" data-testid="agent-form-dialog">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{t('settings.agents.subtitle')}</DialogDescription>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-1.5">
            <Label className="text-[12.5px] text-tx-secondary font-medium">
              {t('settings.agents.name')}
            </Label>
            <Input
              type="text"
              autoComplete="off"
              {...register('name')}
              data-testid="agent-name"
            />
            {errors.name?.message && (
              <p role="alert" className="text-[11.5px] text-status-danger" data-testid="agent-name-error">
                {t(errors.name.message)}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label className="text-[12.5px] text-tx-secondary font-medium">
              {t('settings.agents.url')}
            </Label>
            <Input
              type="password"
              autoComplete="off"
              placeholder={
                isEdit ? t('settings.agents.urlKeepPlaceholder') : t('settings.agents.urlHint')
              }
              {...register('url')}
              data-testid="agent-url"
            />
            {isEdit && agent?.configured && (
              <span className="text-[11.5px] text-tx-faint" data-testid="agent-configured-hint">
                {t('settings.agents.configuredHint', { scheme: agent.scheme || '—' })}
              </span>
            )}
            {!isEdit && (
              <span className="text-[11.5px] text-tx-faint">{t('settings.agents.urlHint')}</span>
            )}
            {errors.url?.message && (
              <p role="alert" className="text-[11.5px] text-status-danger" data-testid="agent-url-error">
                {t(errors.url.message)}
              </p>
            )}
          </div>

          <div className="flex items-center gap-2">
            <Controller
              control={control}
              name="enabled"
              render={({ field }) => (
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  data-testid="agent-enabled"
                />
              )}
            />
            <Label className="text-[12.5px]">{t('settings.agents.enabled')}</Label>
          </div>

          <div className="flex flex-col gap-2">
            <Label className="text-[12.5px] text-tx-secondary font-medium">
              {t('settings.agents.eventTypes.title')}
            </Label>
            <Controller
              control={control}
              name="event_types"
              render={({ field }) => (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  {EVENT_TYPES.map((et) => {
                    const checked = field.value.includes(et);
                    return (
                      <label
                        key={et}
                        className="flex items-center gap-2 text-[12.5px] text-tx-secondary cursor-pointer"
                      >
                        <Checkbox
                          checked={checked}
                          onCheckedChange={(c) => {
                            const set = new Set(field.value);
                            if (c === true) set.add(et);
                            else set.delete(et);
                            field.onChange([...set]);
                          }}
                          data-testid={`agent-event-${et}`}
                        />
                        {t(EVENT_TYPE_LABEL_KEY[et] ?? et)}
                      </label>
                    );
                  })}
                </div>
              )}
            />
          </div>
        </form>

        <DialogFooter className="flex items-center gap-2">
          {canTest && (
            <Button
              type="button"
              variant="outline"
              className="mr-auto"
              onClick={() => {
                if (agent?.id != null) test.mutate(agent.id);
              }}
              disabled={testingThis}
              data-testid="agent-test"
            >
              {testingThis ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />
              ) : (
                <Send className="w-3.5 h-3.5 mr-1.5" />
              )}
              {t('settings.agents.test')}
            </Button>
          )}
          {isEdit && !agent?.configured && (
            <Badge variant="neutral" className="mr-auto" data-testid="agent-test-unavailable">
              {t('settings.agents.testNeedsSave')}
            </Badge>
          )}
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button
            type="button"
            variant="primary"
            onClick={() => { void onSubmit(); }}
            disabled={isSubmitting || create.isPending || update.isPending}
            data-testid="agent-save"
          >
            {(isSubmitting || create.isPending || update.isPending) && (
              <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />
            )}
            {t('settings.agents.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
