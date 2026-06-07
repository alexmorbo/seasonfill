import { useTranslation } from 'react-i18next';
import { Radar, Plus, Settings as SettingsIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useNavigate } from 'react-router-dom';

export function ScansFirstRunState({ onTriggerScan }: { onTriggerScan?: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <div data-testid="scans-first-run" className="empty max-w-[500px] mx-auto mt-6 p-8 text-center">
      <div className="flex justify-center mb-4 text-faint">
        <Radar className="w-10 h-10" aria-hidden="true" />
      </div>
      <h2 className="text-[18px] font-semibold mb-2">{t('scans.firstRun.title')}</h2>
      <p className="text-[13.5px] text-muted mb-4 leading-snug">{t('scans.firstRun.body')}</p>
      <div className="flex gap-2 justify-center">
        <Button variant="default" size="sm" onClick={onTriggerScan}>
          <Plus className="w-3.5 h-3.5 mr-1" aria-hidden="true" />
          {t('scans.firstRun.cta')}
        </Button>
        <Button variant="outline" size="sm" onClick={() => navigate('/settings')}>
          <SettingsIcon className="w-3.5 h-3.5 mr-1" aria-hidden="true" />
          {t('scans.firstRun.configureCron')}
        </Button>
      </div>
    </div>
  );
}
