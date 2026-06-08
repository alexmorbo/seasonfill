// reason: test-only file, never loaded by Fast Refresh in the running app
/* eslint-disable react-refresh/only-export-components */
import { type ReactElement } from 'react';
import { render } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { PageTitleProvider, usePageTitle } from '@/components/shell/page-title-context';
import { InstanceFilterProvider } from '@/lib/instance-filter-context';

function TitleProbe({ onTitle }: { onTitle: (t: string) => void }) {
  const { title } = usePageTitle();
  onTitle(title);
  return null;
}

/**
 * Renders a page inside PageTitleProvider + router + react-query + i18n
 * providers. Returns a `getTitle()` probe that exposes the current
 * provider title so per-page tests can assert that the page called
 * `useSetPageTitle(...)` with the right i18n key.
 */
export function renderPageWithTitle(
  ui: ReactElement,
  opts: { route?: string } = {},
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  let latestTitle = '__INITIAL__';
  const handler = (t: string) => {
    latestTitle = t;
  };
  const result = render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={[opts.route ?? '/']}>
          <InstanceFilterProvider>
            <PageTitleProvider defaultTitle="__INITIAL__">
              <TitleProbe onTitle={handler} />
              {ui}
            </PageTitleProvider>
          </InstanceFilterProvider>
        </MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>,
  );
  return { ...result, qc, getTitle: () => latestTitle };
}
