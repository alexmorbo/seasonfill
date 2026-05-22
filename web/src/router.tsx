import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { ProtectedLayout } from '@/components/ProtectedLayout';
import { Placeholder } from '@/components/Placeholder';
import { Login } from '@/pages/Login';

export const router = createBrowserRouter([
  { path: '/login', element: <Login /> },
  {
    element: <ProtectedRoute><ProtectedLayout /></ProtectedRoute>,
    children: [
      { path: '/',          element: <Placeholder title="Dashboard" /> },
      { path: '/scans',     element: <Placeholder title="Scans" /> },
      { path: '/decisions', element: <Placeholder title="Decisions" /> },
      { path: '/grabs',     element: <Placeholder title="Grabs" /> },
      { path: '/instances', element: <Placeholder title="Instances" /> },
    ],
  },
  { path: '*', element: <Navigate to="/" replace /> },
]);
