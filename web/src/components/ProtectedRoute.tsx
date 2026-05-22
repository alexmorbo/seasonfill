import { Navigate } from 'react-router-dom';
import { useAuth } from '@/lib/auth';

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { state } = useAuth();
  if (state === 'pending') {
    return <div className="grid place-items-center h-screen text-faint mono">checking session…</div>;
  }
  if (state === 'unauthenticated') return <Navigate to="/login" replace />;
  return <>{children}</>;
}
