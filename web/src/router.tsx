import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { ProtectedLayout } from '@/components/ProtectedLayout';
import { Placeholder } from '@/components/Placeholder';
import { Login } from '@/pages/Login';
import { Dashboard } from '@/pages/Dashboard';
import { Instances } from '@/pages/Instances';
import { Scans } from '@/pages/Scans';

export const router = createBrowserRouter([
  { path: '/login', element: <Login /> },
  {
    element: <ProtectedRoute><ProtectedLayout /></ProtectedRoute>,
    children: [
      { path: '/',          element: <Dashboard /> },
      { path: '/scans',     element: <Scans /> },
      { path: '/decisions', element: <Placeholder title="Decisions" /> },
      { path: '/grabs',     element: <Placeholder title="Grabs" /> },
      { path: '/instances', element: <Instances /> },
    ],
  },
  { path: '*', element: <Navigate to="/" replace /> },
]);
