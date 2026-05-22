import { NavLink } from 'react-router-dom';
import { LayoutDashboard, ListTree, GitBranch, Download, Server, Plus } from 'lucide-react';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Button } from '@/components/ui/button';
import { useInstances } from '@/lib/instances';
import { cn } from '@/lib/utils';

const NAV = [
  { to: '/',          label: 'Dashboard', icon: LayoutDashboard, key: 'dashboard' },
  { to: '/scans',     label: 'Scans',     icon: ListTree,        key: 'scans' },
  { to: '/decisions', label: 'Decisions', icon: GitBranch,       key: 'decisions' },
  { to: '/grabs',     label: 'Grabs',     icon: Download,        key: 'grabs' },
  { to: '/instances', label: 'Instances', icon: Server,          key: 'instances' },
] as const;

function NavList({ onNewScan, onNavigate }: { onNewScan: () => void; onNavigate?: () => void }) {
  const { data } = useInstances();
  const count = data?.instances?.length;
  return (
    <nav className="flex flex-col gap-0.5 h-full">
      <div className="px-2.5 pt-2 pb-1.5 text-[11px] uppercase tracking-[0.08em] text-faint">
        Navigation
      </div>
      {NAV.map((item) => (
        <NavLink
          key={item.key}
          to={item.to}
          end={item.to === '/'}
          onClick={() => onNavigate?.()}
          className={({ isActive }) =>
            cn(
              'flex items-center gap-2.5 px-2.5 py-2 rounded-md text-[13.5px] text-foreground-2',
              'hover:bg-surface hover:text-foreground',
              isActive && 'bg-surface-2 text-foreground shadow-[inset_2px_0_0_oklch(var(--accent))]',
            )
          }
        >
          <item.icon className="w-4 h-4 shrink-0 text-muted" />
          <span>{item.label}</span>
          {item.key === 'instances' && count !== undefined && (
            <span className="ml-auto font-mono text-[11px] text-faint">{count}</span>
          )}
        </NavLink>
      ))}
      <div className="flex-1" />
      <Button
        type="button"
        onClick={() => {
          onNewScan();
          onNavigate?.();
        }}
        className="mt-2 h-9 gap-2 font-semibold text-[13px]"
      >
        <Plus className="w-3.5 h-3.5" /> New Scan
      </Button>
    </nav>
  );
}

export function DesktopSidebar({ onNewScan }: { onNewScan: () => void }) {
  return (
    <aside className="hidden md:flex flex-col gap-0.5 w-[240px] shrink-0 border-r border-border bg-bg p-3 overflow-y-auto">
      <NavList onNewScan={onNewScan} />
    </aside>
  );
}

export function MobileSidebar({
  open,
  onOpenChange,
  onNewScan,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onNewScan: () => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="left" className="w-[240px] p-3 bg-bg border-r border-border">
        <SheetHeader className="sr-only">
          <SheetTitle>Navigation</SheetTitle>
        </SheetHeader>
        <NavList onNewScan={onNewScan} onNavigate={() => onOpenChange(false)} />
      </SheetContent>
    </Sheet>
  );
}
