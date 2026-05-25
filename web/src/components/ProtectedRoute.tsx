import { Navigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSession } from '@/lib/auth';

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const { isPending, isError } = useSession();
  if (isPending) {
    return <div className="grid place-items-center h-screen text-faint mono">{t('common.checkingSession')}</div>;
  }
  if (isError) return <Navigate to="/login" replace />;
  return <>{children}</>;
}
