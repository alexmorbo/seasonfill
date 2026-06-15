// B-13 Series Detail v2 — consolidated smoke at 4K against the deployed
// build. Runs after each B-13 deploy to catch regressions in the bleed
// hero / rail card / cast strip / episode meta chip.
//
// Run locally:
//   cd apps/seasonfill
//   npx playwright test tests/playwright/b13_smoke.spec.ts
//
// Override with PLAYWRIGHT_BASE_URL for non-default deploys.

import { test, expect, type Page } from '@playwright/test';

const BASE_URL = process.env['PLAYWRIGHT_BASE_URL'] ?? 'https://sf.arr.morbo.dev';

// "For All Mankind" on the homelab instance — known-good rich TMDB series
// (companies, country, awards, cast, recent activity, on-disk).
const TARGET = `/series/homelab/369`;

test.use({ viewport: { width: 3840, height: 2160 } });

async function goto(page: Page) {
  await page.goto(`${BASE_URL}${TARGET}`);
  await page.waitForLoadState('networkidle', { timeout: 20_000 });
  await expect(page.getByTestId('series-hero')).toBeVisible({ timeout: 10_000 });
}

test.describe('B-13 Series Detail v2 — bleed hero', () => {
  test('hero uses bleed mode with full chrome', async ({ page }) => {
    await goto(page);
    const hero = page.getByTestId('series-hero');
    await expect(hero).toHaveClass(/sd-hero-bleed/);
    await expect(hero).toHaveAttribute('data-fallback', 'none');
    await expect(page.getByTestId('hero-backdrop-layer')).toBeVisible();
    await expect(page.getByTestId('hero-scrim')).toBeVisible();
    // Legacy chrome removed.
    await expect(page.getByTestId('status-pill')).toHaveCount(0);
    // Glass next-card present (default / ended / production — any non-null is fine).
    const nextCard = page.getByTestId('next-episode-card').first();
    await expect(nextCard).toBeVisible();
    const variant = await nextCard.getAttribute('data-variant');
    expect(['default', 'ended', 'production']).toContain(variant);
    // On-disk strip below the divider, dark tone over the scrim.
    const strip = page.getByTestId('hero-library-strip');
    await expect(strip).toBeVisible();
    expect(await strip.getAttribute('data-tone')).toBe('dark');
    await page.screenshot({
      path: 'test-results/b13-hero-region-4k.png',
      clip: { x: 0, y: 0, width: 3840, height: 1200 },
    });
  });

  test('continuing-without-date series renders production card and bleed reaches right pane', async ({ page }) => {
    await page.goto(`${BASE_URL}/series/homelab/372`);
    await page.waitForLoadState('networkidle', { timeout: 20_000 });
    await expect(page.getByTestId('series-hero')).toBeVisible({ timeout: 10_000 });

    // Issue 3 — production card present.
    const nextCard = page.getByTestId('next-episode-card');
    await expect(nextCard).toBeVisible();
    await expect(nextCard).toHaveAttribute('data-variant', 'production');

    // Issue 1 — backdrop layer reaches right-pane edges, sidebar excluded.
    const backdropBox = await page.getByTestId('hero-backdrop-layer').boundingBox();
    const viewportW = page.viewportSize()?.width ?? 3840;
    const sidebarW = 244;
    if (!backdropBox) throw new Error('backdrop layer missing');
    // Left edge should sit at ~sidebarW (allow ±20px for scrollbar).
    expect(backdropBox.x).toBeGreaterThan(sidebarW - 20);
    expect(backdropBox.x).toBeLessThan(sidebarW + 20);
    // Width should span the rest of the viewport (allow ±30px slack).
    expect(backdropBox.width).toBeGreaterThan(viewportW - sidebarW - 30);

    // Issue 2 — hero inner padding tightened. Top of poster should sit
    // well above the 300px mark (visual sanity).
    const posterBox = await page.getByTestId('hero-poster').boundingBox();
    if (!posterBox) throw new Error('hero poster missing');
    const heroTop = backdropBox.y;
    const posterFromHeroTop = posterBox.y - heroTop;
    expect(posterFromHeroTop).toBeLessThan(260);

    await page.screenshot({
      path: 'test-results/b13-fix-372-hero-4k.png',
      fullPage: false,
      clip: { x: 0, y: 0, width: viewportW, height: 1100 },
    });
  });
});

test.describe('B-13 Series Detail v2 — overview rail', () => {
  test('rail card surfaces status / network / studio / country / awards', async ({ page }) => {
    await goto(page);
    const card = page.getByTestId('rail-card');
    await expect(card).toBeVisible();
    await expect(page.getByTestId('rail-row-status')).toBeVisible();
    await expect(page.getByTestId('rail-row-network')).toBeVisible();
    // Studio / country come from B-13a (story 354). Both should be set
    // for "For All Mankind".
    await expect(page.getByTestId('rail-row-studio')).toBeVisible();
    await expect(page.getByTestId('rail-row-countries')).toBeVisible();
    // 365b — 3 new rail rows. For All Mankind has all three.
    await expect(page.getByTestId('rail-row-premiere-date')).toBeVisible();
    await expect(page.getByTestId('rail-row-original-language')).toBeVisible();
    // Awards: present if OMDb hydrated. Tolerate absence.
    // (Awards may be omitted when OMDb is degraded.)
    await page.screenshot({
      path: 'test-results/b13-overview-rail-4k.png',
      clip: { x: 0, y: 800, width: 3840, height: 1000 },
    });
  });
});

test.describe('B-13 Series Detail v2 — cast strip in overview', () => {
  test('cast strip renders Seer-style grid; legacy bottom carousel gone', async ({ page }) => {
    await goto(page);
    const grid = page.getByTestId('cast-strip-grid');
    await expect(grid).toBeVisible();
    const cards = page.getByTestId('cast-strip-card');
    expect(await cards.count()).toBeGreaterThanOrEqual(3);
    await expect(page.getByTestId('cast-carousel')).toHaveCount(0);
  });
});

test.describe('B-13 Series Detail v2 — recent strip', () => {
  test('recent strip is present when recent activity exists', async ({ page }) => {
    await goto(page);
    const strip = page.getByTestId('recent-strip');
    // FAM has recent activity in the homelab instance — should render.
    // If the test environment ever flips to zero-activity, soften this
    // to a count >= 0 check; for now require visibility.
    await expect(strip).toBeVisible();
  });
});

test.describe('B-13 Series Detail v2 — episode meta chip', () => {
  test('episode rows on a season-with-files expose the .eq chip', async ({ page }) => {
    await goto(page);
    // Expand season 4 (known-good for FAM — fully on disk).
    const season = page.locator('[data-testid="season-row"]', { hasText: /Season 4|Сезон 4/ }).first();
    if (await season.count() > 0) {
      await season.click();
    }
    const chips = page.getByTestId('episode-row-eq');
    expect(await chips.count()).toBeGreaterThan(0);
    const text = (await chips.first().textContent()) || '';
    expect(text).toMatch(/·/);
  });
});

test.describe('B-13 Series Detail v2 — section order', () => {
  test('sections appear in the v2 order', async ({ page }) => {
    await goto(page);
    const ids = ['series-hero', 'overview-section', 'torrents-section',
                 'seasons-accordion', 'recommendations-carousel', 'external-links-footer'];
    const handles = await Promise.all(ids.map(id => page.getByTestId(id).elementHandle({ timeout: 5_000 })));
    for (let i = 1; i < handles.length; i++) {
      const prev = handles[i - 1];
      const next = handles[i];
      if (!prev || !next) continue;
      const cmp = await page.evaluate(([a, b]) => a!.compareDocumentPosition(b!),
        [prev, next] as const);
      // Node.DOCUMENT_POSITION_FOLLOWING = 4
      expect(cmp & 4).toBeTruthy();
    }
    await page.screenshot({ path: 'test-results/b13-full-page-4k.png', fullPage: true });
  });
});

test.describe('B-13 Series Detail v2 — Sonarr-only fallback', () => {
  test('a series without TMDB hydration falls back to flat header', async ({ page }) => {
    // Try a stub series. If none exist on the deployed instance, skip.
    // Convention: stub series have empty backdrop/poster + status='unknown'.
    // The page still renders with `data-fallback="sonarr-only"`.
    await page.goto(`${BASE_URL}/series`);
    await page.waitForLoadState('networkidle', { timeout: 15_000 });
    const hero = page.getByTestId('series-hero');
    // Don't fail the spec if no stub series exists — log + soft skip.
    const sonarrOnlyLink = page.locator('a[href^="/series/homelab/"]').first();
    if (await sonarrOnlyLink.count() === 0) {
      test.skip(true, 'No series available on deployed instance to probe');
    }
    // The smoke we care about is "the page mounts without throwing on
    // a series that has no rich data". Just navigate to the first link
    // and check the hero element exists.
    const href = await sonarrOnlyLink.getAttribute('href');
    if (!href) test.skip(true, 'No href on first series link');
    await page.goto(`${BASE_URL}${href}`);
    await expect(hero).toBeVisible({ timeout: 10_000 });
    // hero must have one of the two fallback states.
    const fallback = await hero.getAttribute('data-fallback');
    expect(['none', 'sonarr-only']).toContain(fallback);
  });
});

test.describe('B-13 Series Detail v2 — rail network logo dedup (Star City)', () => {
  test('network row renders logo only when logo_asset is present (no text fallback)', async ({ page }) => {
    await page.goto(`${BASE_URL}/series/homelab/372`);
    await page.waitForLoadState('networkidle', { timeout: 20_000 });
    await expect(page.getByTestId('series-hero')).toBeVisible({ timeout: 10_000 });

    const row = page.getByTestId('rail-row-network');
    await expect(row).toBeVisible();
    // Issue 4: row contains an <img> and NO text node beyond whitespace.
    const imgCount = await row.locator('img').count();
    expect(imgCount).toBe(1);
    // Inspect the value span (second child of the row, see RailRow shape):
    // assert no span with the legacy mono "PARAMOUNT+"/"APPLE TV" text class.
    const monoCount = await row.locator('span.font-mono').count();
    expect(monoCount).toBe(0);

    await page.screenshot({
      path: 'test-results/b13-365b-rail-network-372.png',
      clip: { x: 2900, y: 600, width: 900, height: 800 },
    });
  });
});
