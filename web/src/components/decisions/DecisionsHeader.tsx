import { useTranslation } from 'react-i18next';
import type { DecisionsWindow } from '@/lib/api/decisions';

export interface DecisionsHeaderProps {
  readonly window: DecisionsWindow;
  readonly decisionsCount: number;
  readonly seriesCount: number;
}

const WINDOW_LABEL_KEY: Record<DecisionsWindow, string> = {
  '24h': 'decisions.window.h24',
  '7d':  'decisions.window.d7',
  '30d': 'decisions.window.d30',
  'all': 'decisions.window.all',
};

export function DecisionsHeader({
  window, decisionsCount, seriesCount,
}: DecisionsHeaderProps) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-3 flex-wrap mb-4">
      <h2 className="text-[15px] font-semibold tracking-tight">
        {t('decisions.headerTitle')}
      </h2>
      <span className="font-mono text-[11.5px] text-tx-faint">
        {t('decisions.headerCount', {
          window: t(WINDOW_LABEL_KEY[window]),
          decisions: decisionsCount,
          seriesCount,
        })}
      </span>
    </div>
  );
}
