import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { ProtectedLayout } from '@/components/ProtectedLayout';
import { Login } from '@/pages/Login';
import { Dashboard } from '@/pages/Dashboard';
import { Instances } from '@/pages/Instances';
import { InstanceQueue } from '@/pages/InstanceQueue';
import { Scans } from '@/pages/Scans';
import { ScanDetail } from '@/pages/ScanDetail';
import { Decisions } from '@/pages/Decisions';
import { Grabs } from '@/pages/Grabs';
import { Watchdog } from '@/pages/Watchdog';
import { Health } from '@/pages/Health';
import { Gaps } from '@/pages/Gaps';
import { Calendar } from '@/pages/Calendar';
import { Stats } from '@/pages/Stats';
import { Lists } from '@/pages/Lists';
import { Collections } from '@/pages/Collections';
import { SettingsPage } from '@/pages/SettingsPage';
import { SettingsRedirect } from '@/pages/settings/SettingsRedirect';
import { ProfileTab } from '@/pages/settings/ProfileTab';
import { SystemLayout } from '@/pages/settings/SystemLayout';
import { SystemTabGuard } from '@/components/settings/SystemTabGuard';
import { GeneralTab } from '@/components/settings/GeneralTab';
import { SecurityTab } from '@/components/settings/SecurityTab';
import { IntegrationsTab } from '@/components/settings/IntegrationsTab';
import { AgentsTab } from '@/pages/settings/AgentsTab';
import { BlocklistTab } from '@/pages/settings/BlocklistTab';
import { SettingsExternalServices } from '@/pages/SettingsExternalServices';
import { Series } from '@/pages/Series';
import { SeriesDetail } from '@/pages/SeriesDetail';
import { SeriesCast } from '@/pages/SeriesCast';
import { Movies } from '@/pages/Movies';
import { MovieDetail } from '@/pages/MovieDetail';
import { MovieCast } from '@/pages/MovieCast';
import { LegacySeriesRedirect } from '@/pages/LegacySeriesRedirect';
import { Person } from '@/pages/Person';
import { DiscoveryPage } from '@/pages/DiscoveryPage';
import { SearchPage } from '@/pages/SearchPage';
import { Requests } from '@/pages/Requests';
import { Users } from '@/pages/Users';
import { NotFound } from '@/pages/NotFound';

export const router = createBrowserRouter([
  { path: '/login', element: <Login /> },
  {
    element: <ProtectedRoute><ProtectedLayout /></ProtectedRoute>,
    children: [
      { path: '/',          element: <Dashboard /> },
      { path: '/scans',     element: <Scans /> },
      { path: '/scans/:id', element: <ScanDetail /> },
      { path: '/decisions', element: <Decisions /> },
      { path: '/grabs',     element: <Grabs /> },
      { path: '/series',    element: <Series /> },
      { path: '/series/:id', element: <SeriesDetail /> },
      { path: '/series/:id/cast', element: <SeriesCast /> },
      // REMOVE 2026-09: soft-redirect for pre-N-1e operator bookmarks.
      // Story 495 §A2 — keeps `/series/:instance/:id` working for one
      // release cycle; LegacySeriesRedirect navigates to the new shape.
      { path: '/series/:instance/:id', element: <LegacySeriesRedirect /> },
      { path: '/series/:instance/:id/cast', element: <LegacySeriesRedirect kind="cast" /> },
      { path: '/movies',          element: <Movies /> },
      { path: '/movies/:tmdbId',  element: <MovieDetail /> },
      { path: '/movies/:tmdbId/cast', element: <MovieCast /> },
      { path: '/person/:tmdbId',            element: <Person /> },
      { path: '/discovery', element: <DiscoveryPage /> },
      { path: '/search', element: <SearchPage /> },
      // /requests is admin-only. The <Requests /> page self-gates on
      // role === 'admin' (renders a localized denied panel otherwise), so no
      // route-level guard wrapper is needed. U-6b will broaden this to a
      // bool-permission once /me surfaces perms.
      { path: '/requests', element: <Requests /> },
      // /users is gated on the manage_users permission (or role === 'admin').
      // The <Users /> page self-gates and renders a localized denied panel
      // otherwise, so no route-level guard wrapper is needed.
      { path: '/users', element: <Users /> },
      { path: '/calendar',  element: <Calendar /> },
      { path: '/watchdog',  element: <Watchdog /> },
      { path: '/health',    element: <Health /> },
      { path: '/gaps',      element: <Gaps /> },
      { path: '/stats',     element: <Stats /> },
      { path: '/lists',     element: <Lists /> },
      { path: '/collections', element: <Collections /> },
      { path: '/instances',             element: <Instances /> },
      { path: '/instances/:name/queue', element: <InstanceQueue /> },
      {
        path: '/settings',
        element: <SettingsPage />,
        children: [
          { index: true, element: <SettingsRedirect /> },
          { path: 'profile', element: <ProfileTab /> },
          // Ф8-U-6c — notification agents are per-user; the page lives OUTSIDE
          // the admin-only SystemTabGuard subtree, as a sibling of profile.
          { path: 'agents', element: <AgentsTab /> },
          {
            path: 'system',
            element: <SystemTabGuard><SystemLayout /></SystemTabGuard>,
            children: [
              { index: true, element: <Navigate to="general" replace /> },
              { path: 'general',      element: <GeneralTab /> },
              { path: 'security',     element: <SecurityTab /> },
              { path: 'integrations', element: <IntegrationsTab /> },
              { path: 'blocklist', element: <BlocklistTab /> },
            ],
          },
        ],
      },
      // /settings/external-services stays as a sibling route in N-7b.
      // The move under /settings/system/external-services is deferred
      // to N-7c per Decision §1 in story 486 (avoids cross-cutting
      // navigation/link rewrites in N-7b scope).
      { path: '/settings/external-services', element: <SettingsExternalServices /> },
    ],
  },
  { path: '*', element: <NotFound /> },
]);
