import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { TimezoneProvider } from '@/lib/timezone';
import { queryClient } from '@/lib/query-client';
import { router } from '@/router';
import '@/i18n';
import '@/index.css';

const rootEl = document.getElementById('root');
if (!rootEl) throw new Error('root element missing');

// TooltipProvider sits between QueryClientProvider and the router so
// any component anywhere in the tree (authed or unauthed pages) can
// use <Tooltip>. delayDuration=150ms — see story 016 §3.1.
createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <TimezoneProvider>
        <TooltipProvider delayDuration={150}>
          <RouterProvider router={router} />
          <Toaster richColors closeButton position="bottom-right" />
        </TooltipProvider>
      </TimezoneProvider>
    </QueryClientProvider>
  </StrictMode>,
);
