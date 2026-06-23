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
import { SettingsPage } from '@/pages/SettingsPage';
import { SettingsRedirect } from '@/pages/settings/SettingsRedirect';
import { ProfileTab } from '@/pages/settings/ProfileTab';
import { SystemLayout } from '@/pages/settings/SystemLayout';
import { SystemTabGuard } from '@/components/settings/SystemTabGuard';
import { GeneralTab } from '@/components/settings/GeneralTab';
import { SecurityTab } from '@/components/settings/SecurityTab';
import { IntegrationsTab } from '@/components/settings/IntegrationsTab';
import { SettingsExternalServices } from '@/pages/SettingsExternalServices';
import { Series } from '@/pages/Series';
import { SeriesDetail } from '@/pages/SeriesDetail';
import { SeriesCast } from '@/pages/SeriesCast';
import { LegacySeriesRedirect } from '@/pages/LegacySeriesRedirect';
import { Person } from '@/pages/Person';

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
      { path: '/person/:tmdbId',            element: <Person /> },
      { path: '/watchdog',  element: <Watchdog /> },
      { path: '/instances',             element: <Instances /> },
      { path: '/instances/:name/queue', element: <InstanceQueue /> },
      {
        path: '/settings',
        element: <SettingsPage />,
        children: [
          { index: true, element: <SettingsRedirect /> },
          { path: 'profile', element: <ProfileTab /> },
          {
            path: 'system',
            element: <SystemTabGuard><SystemLayout /></SystemTabGuard>,
            children: [
              { index: true, element: <Navigate to="general" replace /> },
              { path: 'general',      element: <GeneralTab /> },
              { path: 'security',     element: <SecurityTab /> },
              { path: 'integrations', element: <IntegrationsTab /> },
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
  { path: '*', element: <Navigate to="/" replace /> },
]);
