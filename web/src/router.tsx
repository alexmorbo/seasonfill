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
      { path: '/watchdog',  element: <Watchdog /> },
      { path: '/instances',             element: <Instances /> },
      { path: '/instances/:name/queue', element: <InstanceQueue /> },
      { path: '/settings',  element: <Settings /> },
    ],
  },
  { path: '*', element: <Navigate to="/" replace /> },
]);
