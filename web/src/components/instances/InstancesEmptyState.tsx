import { useTranslation } from 'react-i18next';
import { Server, Plus, BookOpen } from 'lucide-react';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';

export interface InstancesEmptyStateProps {
  readonly onAdd: () => void;
}

/**
 * Zero-instances onboarding card. Three numbered steps + primary
 * "Добавить инстанс" CTA + secondary "Как это работает" (placeholder).
 */
export function InstancesEmptyState({ onAdd }: InstancesEmptyStateProps) {
  const { t } = useTranslation();
  const steps = [
    { n: 1, k: 'step1' as const },
    { n: 2, k: 'step2' as const },
    { n: 3, k: 'step3' as const },
  ];
  return (
    <Card className="max-w-[580px] mx-auto mt-6" data-testid="instances-empty-state">
      <CardContent className="p-6 flex flex-col items-center gap-4 text-center">
        <div className="flex items-center justify-center w-12 h-12 rounded-full bg-bg-surface-2 text-tx-muted">
          <Server className="w-6 h-6" />
        </div>
        <h2 className="text-[18px] font-[650] m-0">{t('instances.empty.title')}</h2>
        <p className="text-tx-muted text-[13.5px] m-0">{t('instances.empty.body')}</p>
        <div className="flex flex-col gap-3 w-full text-left">
          {steps.map(({ n, k }) => (
            <div key={n} className="flex items-start gap-3">
              <span className="flex-none flex items-center justify-center w-6 h-6 rounded-full bg-accent-dim text-accent font-mono text-[12px]">
                {n}
              </span>
              <span className="flex flex-col gap-0.5">
                <b className="text-[13.5px]">{t(`instances.empty.${k}.title`)}</b>
                <span className="text-tx-muted text-[12.5px]">{t(`instances.empty.${k}.body`)}</span>
              </span>
            </div>
          ))}
        </div>
        <div className="flex gap-2 mt-2">
          <Button variant="primary" onClick={onAdd}>
            <Plus className="w-4 h-4 mr-1.5" />
            {t('instances.empty.cta.addInstance')}
          </Button>
          {/* TODO(post-F2): wire `Как это работает` to docs URL. */}
          <Button variant="outline" disabled>
            <BookOpen className="w-4 h-4 mr-1.5" />
            {t('instances.empty.cta.howItWorks')}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
