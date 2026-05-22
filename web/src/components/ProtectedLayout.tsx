import { useEffect, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { TopBar } from './TopBar';
import { DesktopSidebar, MobileSidebar } from './Sidebar';
import { NetBanner } from './NetBanner';
import { NewScanModal } from './NewScanModal';
import { useCmdK } from '@/lib/use-cmdk';
import { InstanceFilterProvider } from '@/lib/instance-filter-context';

export function ProtectedLayout() {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [scanModalOpen, setScanModalOpen] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();

  useCmdK(() => setScanModalOpen(true));

  // ?new=1 acts as a one-shot deep-link into the modal. After we've
  // read it, strip the param so it doesn't re-fire on history nav.
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    if (params.get('new') === '1') {
      setScanModalOpen(true);
      params.delete('new');
      const next = params.toString();
      navigate(
        { pathname: location.pathname, search: next ? `?${next}` : '' },
        { replace: true },
      );
    }
    // intentionally only on mount + on path change — search is consumed inside
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.pathname]);

  return (
    <InstanceFilterProvider>
      <div className="h-screen flex flex-col bg-bg">
        <TopBar onMenuClick={() => setMobileOpen(true)} />
        <div className="flex-1 flex min-h-0">
          <DesktopSidebar onNewScan={() => setScanModalOpen(true)} />
          <MobileSidebar
            open={mobileOpen}
            onOpenChange={setMobileOpen}
            onNewScan={() => setScanModalOpen(true)}
          />
          <main className="flex-1 overflow-y-auto bg-bg">
            <Outlet />
          </main>
        </div>
        <NetBanner />
        <NewScanModal open={scanModalOpen} onOpenChange={setScanModalOpen} />
      </div>
    </InstanceFilterProvider>
  );
}
