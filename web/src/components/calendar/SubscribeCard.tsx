import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CalendarPlus, Check, Copy, ExternalLink } from 'lucide-react';
import { toast } from 'sonner';

import { mintICSToken, revokeICSTokens, type ICSScope, type ICSToken } from '@/api/calendar';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

const SCOPES: ICSScope[] = ['all', 'library', 'followed'];

// SubscribeCard exposes the ICS calendar feed (Ф3 S3): mint a signed
// subscription URL to paste into Google/Apple Calendar and revoke every prior
// URL. Revocation uses a SEPARATE epoch, so it never logs out the operator.
export function SubscribeCard() {
  const { t } = useTranslation();
  const [scope, setScope] = useState<ICSScope>('all');
  const [token, setToken] = useState<ICSToken | null>(null);
  const [minting, setMinting] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [copied, setCopied] = useState(false);

  const scopeLabel = (s: ICSScope): string =>
    s === 'library'
      ? t('calendar.subscribe.scopeLibrary')
      : s === 'followed'
        ? t('calendar.subscribe.scopeFollowed')
        : t('calendar.subscribe.scopeAll');

  const onMint = async () => {
    setMinting(true);
    try {
      const res = await mintICSToken(scope);
      setToken(res);
      setCopied(false);
    } catch {
      toast.error(t('calendar.subscribe.mintFailed'));
    } finally {
      setMinting(false);
    }
  };

  const onCopy = async () => {
    if (!token) return;
    try {
      await navigator.clipboard?.writeText(token.ics_url);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard denied (insecure context / permissions) — leave URL visible
      // so the operator can select it manually.
    }
  };

  const onRevoke = async () => {
    setRevoking(true);
    try {
      await revokeICSTokens();
      setToken(null); // the shown URL is now dead
      setCopied(false);
      toast.success(t('calendar.subscribe.revoked'));
    } catch {
      toast.error(t('calendar.subscribe.revokeFailed'));
    } finally {
      setRevoking(false);
    }
  };

  return (
    <Card className="flex flex-col gap-3 p-4" data-testid="calendar-subscribe-card">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="mr-auto text-[13px] font-semibold text-tx-primary">
          {t('calendar.subscribe.title')}
        </h2>

        <label className="flex items-center gap-1.5 text-[12px] text-tx-muted">
          <span className="sr-only sm:not-sr-only">{t('calendar.subscribe.scope')}</span>
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as ICSScope)}
            data-testid="ics-scope-select"
            className="h-7 rounded-md border border-border-strong bg-bg-base px-2 text-[12px] text-tx-primary focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
          >
            {SCOPES.map((s) => (
              <option key={s} value={s}>
                {scopeLabel(s)}
              </option>
            ))}
          </select>
        </label>

        <Button
          variant="primary"
          size="sm"
          onClick={onMint}
          disabled={minting}
          data-testid="ics-subscribe-btn"
        >
          <CalendarPlus className="size-4" />
          {t('calendar.subscribe.cta')}
        </Button>
      </div>

      {token ? (
        <div className="flex flex-col gap-2" data-testid="ics-result">
          <div className="flex flex-wrap items-center gap-2">
            <Input
              readOnly
              value={token.ics_url}
              onFocus={(e) => e.currentTarget.select()}
              data-testid="ics-url-input"
              className="h-8 min-w-0 flex-1 font-mono text-[12px]"
              aria-label={t('calendar.subscribe.title')}
            />
            <Button
              variant="outline"
              size="sm"
              onClick={onCopy}
              data-testid="ics-copy-btn"
              className={cn(copied && 'text-ok')}
            >
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
              {copied ? t('calendar.subscribe.copied') : t('calendar.subscribe.copy')}
            </Button>
            <Button variant="outline" size="sm" asChild data-testid="ics-open-link">
              <a href={token.webcal_url}>
                <ExternalLink className="size-4" />
                {t('calendar.subscribe.openInApp')}
              </a>
            </Button>
          </div>
          <p className="text-[12px] leading-relaxed text-tx-muted">
            {t('calendar.subscribe.instructions')}
          </p>
        </div>
      ) : null}

      <div className="flex items-center">
        <Button
          variant="destructive"
          size="sm"
          onClick={onRevoke}
          disabled={revoking}
          data-testid="ics-revoke-btn"
        >
          {t('calendar.subscribe.revoke')}
        </Button>
      </div>
    </Card>
  );
}
