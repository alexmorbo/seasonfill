import { TodayCard } from './TodayCard';
import { AlertsCard } from './AlertsCard';
import { QuickActionsCard } from './QuickActionsCard';
import { WatchdogMiniCard } from './WatchdogMiniCard';

export function DashboardRail() {
  return (
    <aside className="flex flex-col gap-4 sticky top-0" data-testid="dashboard-rail">
      <TodayCard />
      <AlertsCard />
      <QuickActionsCard />
      <WatchdogMiniCard />
    </aside>
  );
}
