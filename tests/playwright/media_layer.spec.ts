// Story 349b — verify the FE has fully moved off the legacy poster
// proxy to the content-addressed `/api/v1/media/<sha256>` handler on
// every list view that previously rendered <SeriesPoster>.
//
// Runs at 4K because density bugs (extra rows / overscan) only surface
// when the viewport is large enough to mount the full list. The spec
// is hosted at the repo root (outside web/tsconfig.json#include) so
// the project does not need a runtime Playwright dependency to ship;
// the spec is executed against the deployed environment via the
// homelab CI smoke job or `npx playwright test` from the repo root.
//
// Run locally:
//   cd apps/seasonfill
//   npm i -D @playwright/test
//   npx playwright install chromium
//   npx playwright test tests/playwright/media_layer.spec.ts
//
// The base URL points at the deployed instance; override via
// PLAYWRIGHT_BASE_URL when smoke-testing against a different deploy.

import { test, expect, type Request } from '@playwright/test';

const BASE_URL =
  process.env['PLAYWRIGHT_BASE_URL'] ?? 'https://sf.arr.morbo.dev';

const ROUTES = ['/dashboard', '/series', '/grabs', '/queue'] as const;

const LEGACY_POSTER_RE =
  /\/api\/v1\/instances\/[^/]+\/series\/\d+\/poster(\?|$)/;
const CONTENT_ADDRESSED_RE = /\/api\/v1\/media\/[0-9a-f]{64}(\?|$)/;

test.use({ viewport: { width: 3840, height: 2160 } });

for (const route of ROUTES) {
  test(`media-layer: ${route} uses content-addressed media`, async ({ page }) => {
    const legacy: string[] = [];
    const contentAddressed: string[] = [];
    const contentAddressedResponses: number[] = [];

    page.on('request', (req: Request) => {
      const url = req.url();
      if (LEGACY_POSTER_RE.test(url)) legacy.push(url);
      if (CONTENT_ADDRESSED_RE.test(url)) contentAddressed.push(url);
    });
    page.on('response', (res) => {
      const url = res.url();
      if (CONTENT_ADDRESSED_RE.test(url)) contentAddressedResponses.push(res.status());
    });

    await page.goto(`${BASE_URL}${route}`);
    await page.waitForLoadState('networkidle', { timeout: 15_000 });

    expect.soft(
      legacy,
      `legacy poster proxy must not be called from ${route}`,
    ).toEqual([]);
    expect(
      contentAddressed.length,
      `content-addressed handler must be called at least once from ${route}`,
    ).toBeGreaterThan(0);
    expect(
      contentAddressedResponses.some((s) => s === 200 || s === 304),
      `at least one /api/v1/media/<hash> response must succeed (got statuses: ${contentAddressedResponses.join(',')})`,
    ).toBe(true);
  });
}
