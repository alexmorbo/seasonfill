import { createBrowserRouter, Navigate, Outlet } from 'react-router-dom';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { PlaceholderShell } from '@/components/PlaceholderShell';
import { Login } from '@/pages/Login';

function Placeholder({ title }: { title: string }) {
  return (
    <div className="max-w-[1440px] mx-auto p-7">
      <h1 className="text-[22px] font-semibold tracking-tight">{title}</h1>
      <p className="text-muted mt-3">Coming in 009d.</p>
    </div>
  );
}

export const router = createBrowserRouter([
  { path: '/login', element: <Login /> },
  {
    element: <ProtectedRoute><PlaceholderShell><Outlet /></PlaceholderShell></ProtectedRoute>,
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
