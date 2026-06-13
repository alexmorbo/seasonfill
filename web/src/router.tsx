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
import { Settings } from '@/pages/Settings';
import { SettingsExternalServices } from '@/pages/SettingsExternalServices';
import { Series } from '@/pages/Series';
import { SeriesDetail } from '@/pages/SeriesDetail';
import { SeriesCast } from '@/pages/SeriesCast';

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
      { path: '/series/:instance/:id', element: <SeriesDetail /> },
      { path: '/series/:instance/:id/cast', element: <SeriesCast /> },
      { path: '/watchdog',  element: <Watchdog /> },
      { path: '/instances',             element: <Instances /> },
      { path: '/instances/:name/queue', element: <InstanceQueue /> },
      { path: '/settings',  element: <Settings /> },
      { path: '/settings/external-services', element: <SettingsExternalServices /> },
    ],
  },
  { path: '*', element: <Navigate to="/" replace /> },
]);
