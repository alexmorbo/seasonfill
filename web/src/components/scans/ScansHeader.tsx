import { useTranslation } from 'react-i18next';

export function ScansHeader({ count, instance }: { count: number; instance: string | null }) {
  const { t } = useTranslation();
  return (
    <header className="flex items-center gap-4 flex-wrap">
      <h2 className="text-[15px] font-semibold tracking-tight">{t('scans.headerTitle')}</h2>
      <span className="font-mono text-[12px] text-faint">
        {t('scans.headerCount', { count })}
        {instance ? ` · ${t('scans.instanceLabel', { name: instance })}` : ''}
      </span>
    </header>
  );
}
