import { useState } from 'react';
import { Outlet } from 'react-router-dom';
import { TopBar } from './TopBar';
import { DesktopSidebar, MobileSidebar } from './Sidebar';
import { NetBanner } from './NetBanner';
import { InstanceFilterProvider } from '@/lib/instance-filter-context';

export function ProtectedLayout() {
  const [mobileOpen, setMobileOpen] = useState(false);
  return (
    <InstanceFilterProvider>
      <div className="h-screen flex flex-col bg-bg">
        <TopBar onMenuClick={() => setMobileOpen(true)} />
        <div className="flex-1 flex min-h-0">
          <DesktopSidebar />
          <MobileSidebar open={mobileOpen} onOpenChange={setMobileOpen} />
          <main className="flex-1 overflow-y-auto bg-bg">
            <Outlet />
          </main>
        </div>
        <NetBanner />
      </div>
    </InstanceFilterProvider>
  );
}
