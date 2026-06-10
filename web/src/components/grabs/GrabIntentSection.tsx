import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import type { components } from '@/api/schema';

export type DecisionIntent = components['schemas']['dto.DecisionIntent'];

// Maps the wire-level `chosen_because` enum value to the i18n key
// suffix. Wire values are snake_case (matches backend dto enum);
// i18n keys are camelCase to match existing style.
const REASON_KEY: Record<string, string> = {
  only_candidate: 'reason.onlyCandidate',
  highest_score: 'reason.highestScore',
  first_pass_quality: 'reason.firstPassQuality',
  watchdog_better_quality: 'reason.watchdogBetterQuality',
  watchdog_better_dub: 'reason.watchdogBetterDub',
  watchdog_better_other: 'reason.watchdogBetterOther',
  watchdog_replay_unregistered: 'reason.watchdogReplayUnregistered',
  watchdog_replay_already_added: 'reason.watchdogReplayAlreadyAdded',
  watchdog_replay_error: 'reason.watchdogReplayError',
  manual_selection: 'reason.manualSelection',
};

// Variant per reason so the badge colour communicates intent.
const REASON_VARIANT: Record<
  string,
  'ok' | 'info' | 'warn' | 'accent' | 'neutral'
> = {
  only_candidate: 'neutral',
  highest_score: 'ok',
  first_pass_quality: 'info',
  watchdog_better_quality: 'info',
  watchdog_better_dub: 'warn',
  watchdog_better_other: 'accent',
  watchdog_replay_unregistered: 'warn',
  watchdog_replay_already_added: 'info',
  watchdog_replay_error: 'warn',
  manual_selection: 'accent',
};

const MAX_HAD_VISIBLE = 8;

export interface GrabIntentSectionProps {
  readonly intent: DecisionIntent | null | undefined;
}

export function GrabIntentSection({ intent }: GrabIntentSectionProps) {
  const { t } = useTranslation();
  if (!intent) return null;

  const target = intent.target_episodes ?? [];
  const had = intent.had_episodes ?? [];
  const chosen = intent.chosen_because ?? '';
  const detail = intent.chosen_reason_detail ?? '';

  const reasonI18nKey = REASON_KEY[chosen];
  const reasonLabel = reasonI18nKey
    ? t(`grabs.intent.${reasonI18nKey}`)
    : chosen;
  const variant = REASON_VARIANT[chosen] ?? 'neutral';

  // Truncate `had` past MAX_HAD_VISIBLE — long backlogs (season pack
  // with 20+ already-present eps) would blow the drawer width.
  const hadVisible =
    had.length > MAX_HAD_VISIBLE ? had.slice(0, MAX_HAD_VISIBLE) : had;
  const hadOverflow = had.length - hadVisible.length;
  const hadOverflowTitle = hadOverflow > 0
    ? had.slice(MAX_HAD_VISIBLE).map((n) => `E${n}`).join(', ')
    : '';

  return (
    <section
      data-testid="drawer-intent-section"
      className="flex flex-col gap-2"
    >
      <span className="text-[10px] font-semibold uppercase tracking-[0.09em] text-tx-faint">
        {t('grabs.intent.title')}
      </span>
      <div className="grid grid-cols-2 gap-3">
        <IntentChipColumn
          label={t('grabs.intent.wanted')}
          empty={t('grabs.intent.wantedNone')}
          episodes={target}
          variant="target"
          testId="drawer-intent-target"
        />
        <IntentChipColumn
          label={t('grabs.intent.had')}
          empty={t('grabs.intent.hadNone')}
          episodes={hadVisible}
          variant="had"
          testId="drawer-intent-had"
          overflowCount={hadOverflow}
          overflowTitle={hadOverflowTitle}
          overflowLabel={t('grabs.intent.more', { count: hadOverflow })}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Badge
          data-testid="drawer-intent-reason"
          variant={variant}
          className="self-start text-[10.5px] font-semibold"
        >
          {reasonLabel}
        </Badge>
        {detail && (
          <p
            data-testid="drawer-intent-reason-detail"
            className="text-xs text-tx-muted leading-snug"
          >
            {detail}
          </p>
        )}
      </div>
    </section>
  );
}

interface IntentChipColumnProps {
  readonly label: string;
  readonly empty: string;
  readonly episodes: readonly number[];
  readonly variant: 'target' | 'had';
  readonly testId: string;
  readonly overflowCount?: number;
  readonly overflowTitle?: string;
  readonly overflowLabel?: string;
}

function IntentChipColumn({
  label,
  empty,
  episodes,
  variant,
  testId,
  overflowCount = 0,
  overflowTitle = '',
  overflowLabel = '',
}: IntentChipColumnProps) {
  const chipClass = variant === 'target'
    ? 'text-info border-info/35 bg-info/12'
    : 'text-tx-muted border-border-subtle bg-bg-surface-2';
  return (
    <div className="flex flex-col gap-1" data-testid={testId}>
      <span className="text-[10px] font-semibold uppercase tracking-[0.06em] text-tx-faint">
        {label}
      </span>
      {episodes.length === 0 ? (
        <span className="text-[11px] italic text-tx-faint">{empty}</span>
      ) : (
        <div className="flex flex-wrap gap-1">
          {episodes.map((n) => (
            <span
              key={n}
              className={cn(
                'inline-flex items-center rounded-[5px] border',
                'px-1.5 py-px font-mono text-[10.5px] font-semibold',
                chipClass,
              )}
            >
              E{n}
            </span>
          ))}
          {overflowCount > 0 && (
            <span
              data-testid={`${testId}-overflow`}
              title={overflowTitle}
              className={cn(
                'inline-flex items-center rounded-[5px] border',
                'px-1.5 py-px font-mono text-[10.5px] font-semibold',
                'text-tx-faint border-border-faint bg-transparent',
              )}
            >
              {overflowLabel}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
