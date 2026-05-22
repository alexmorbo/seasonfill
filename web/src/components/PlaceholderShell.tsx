import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import { logout } from '@/lib/auth';

const VERSION = import.meta.env.VITE_APP_VERSION ?? 'dev';

export function PlaceholderShell({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const onLogout = async () => {
    try { await logout(); } catch { /* ignore */ }
    qc.clear();
    navigate('/login', { replace: true });
  };
  return (
    <div className="min-h-screen flex flex-col bg-bg">
      <header className="flex items-center gap-3 px-5 h-14 border-b border-border">
        <span className="w-[22px] h-[22px] grid place-items-center bg-accent text-accent-text rounded-[5px] font-mono font-bold text-[13px]">sf</span>
        <span className="font-semibold tracking-tight">seasonfill</span>
        <span className="mono text-[11px] text-faint">v{VERSION}</span>
        <span className="flex-1" />
        <Button variant="ghost" onClick={onLogout} className="h-8 px-3 text-[13px]">Logout</Button>
      </header>
      <main className="flex-1 overflow-y-auto">{children}</main>
    </div>
  );
}
