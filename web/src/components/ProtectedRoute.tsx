import { Navigate } from 'react-router-dom';
import { useSession } from '@/lib/auth';

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isPending, isError } = useSession();
  if (isPending) {
    return <div className="grid place-items-center h-screen text-faint mono">checking session…</div>;
  }
  if (isError) return <Navigate to="/login" replace />;
  return <>{children}</>;
}
