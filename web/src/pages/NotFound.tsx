import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router-dom';
import { Home, MapPinOff } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';

/**
 * Catch-all 404 page. Rendered by the `{ path: '*' }` route in
 * `router.tsx`. Stays OUTSIDE ProtectedRoute so logged-out users
 * also see a real 404 instead of bouncing through /login. Story 539
 * (B-47). No `useSetPageTitle` call — the catch-all renders outside
 * `<ProtectedLayout>` (where the PageTitleProvider lives) so the
 * topbar isn't on screen anyway.
 */
export function NotFound() {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  return (
    <div
      data-testid="notfound-stage"
      className="min-h-screen w-full grid place-items-center bg-bg-base px-6 py-6"
    >
      <Card className="w-[420px] max-w-full">
        <CardContent className="flex flex-col items-center gap-4 p-7 text-center">
          <span
            aria-hidden
            className="w-12 h-12 rounded-full bg-bg-surface border border-border-subtle flex items-center justify-center text-tx-secondary"
          >
            <MapPinOff className="w-6 h-6" />
          </span>
          <h1 className="text-[20px] font-semibold tracking-tight">
            {t('notFound.title')}
          </h1>
          <p className="text-[13.5px] text-tx-secondary leading-snug">
            {t('notFound.message')}
          </p>
          <code
            data-testid="notfound-path"
            className="text-[11.5px] font-mono text-tx-faint break-all max-w-full"
          >
            {pathname}
          </code>
          <Button asChild className="h-10 font-semibold gap-2 mt-1">
            <Link to="/" replace data-testid="notfound-home-link">
              <Home className="w-4 h-4" />
              {t('notFound.backHome')}
            </Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
