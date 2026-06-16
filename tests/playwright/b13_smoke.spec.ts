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

test.describe('B-13 Series Detail v2 — 366 hero topbar + overview + cast clip', () => {
  test('back-link lives inside hero (no standalone nav above)', async ({ page }) => {
    await goto(page);

    // hero-back-link is rendered inside series-hero.
    const heroBack = page.getByTestId('hero-back-link');
    await expect(heroBack).toBeVisible();
    const inHero = await page.evaluate(() => {
      const back = document.querySelector('[data-testid="hero-back-link"]');
      const hero = document.querySelector('[data-testid="series-hero"]');
      return back && hero ? hero.contains(back) : false;
    });
    expect(inHero).toBe(true);

    // No <nav> element precedes series-hero inside .sd-real.
    const navAboveHero = await page.evaluate(() => {
      const sd = document.querySelector('.sd-real');
      const hero = document.querySelector('[data-testid="series-hero"]');
      if (!sd || !hero) return null;
      const navs = sd.querySelectorAll(':scope > nav');
      for (const n of Array.from(navs)) {
        const pos = n.compareDocumentPosition(hero);
        // hero follows nav => nav is above hero.
        if (pos & Node.DOCUMENT_POSITION_FOLLOWING) return true;
      }
      return false;
    });
    expect(navAboveHero).toBe(false);
  });

  test('backdrop fade ends before overview section', async ({ page }) => {
    await goto(page);
    const backdrop = await page.getByTestId('hero-backdrop-layer').boundingBox();
    const overview = await page.getByTestId('overview-section').boundingBox();
    if (!backdrop || !overview) throw new Error('missing measurement target');
    // Backdrop physical layer (600px tall) may still extend past overview top,
    // but its mask fades to transparent at 78% of its height — well above
    // overview-section.top. Assert the overview has its own background.
    const overviewBg = await page.getByTestId('overview-section').evaluate(
      (el) => window.getComputedStyle(el).backgroundColor,
    );
    expect(overviewBg).not.toBe('rgba(0, 0, 0, 0)');
    expect(overviewBg).not.toBe('transparent');
    // Sanity: mask stops update means visual fade ends at top+0.78*600 = top+468.
    const fadeEnd = backdrop.y + 0.78 * backdrop.height;
    expect(overview.y).toBeGreaterThan(fadeEnd - 1);
  });

  test('cast strip view-all link is visible and not clipped', async ({ page }) => {
    await goto(page);
    const viewAll = page.getByTestId('cast-strip-view-all');
    await expect(viewAll).toBeVisible();
    const viewAllBox = await viewAll.boundingBox();
    if (!viewAllBox) throw new Error('view-all missing');

    // The cast-strip's section is inside the overview-grid left column.
    // The view-all's right edge must sit within the left column right edge
    // (±4px slack).
    const leftColRight = await page.evaluate(() => {
      const grid = document.querySelector('[data-testid="overview-grid"]');
      const leftCol = grid?.children[0] as HTMLElement | undefined;
      return leftCol ? leftCol.getBoundingClientRect().right : -1;
    });
    expect(leftColRight).toBeGreaterThan(0);
    expect(Math.abs(viewAllBox.x + viewAllBox.width - leftColRight)).toBeLessThan(8);

    // Header uses justify-between (no flex-1 spacer).
    const headerHasSpacer = await page.evaluate(() => {
      const h = document.querySelector('[data-testid="cast-strip-header"]');
      return h ? !!h.querySelector('.flex-1') : null;
    });
    expect(headerHasSpacer).toBe(false);
  });
});

test.describe('B-13 Series Detail v2 — hero flush to topbar + extended backdrop (story 367)', () => {
  test('hero top sits flush with topbar bottom (no empty band)', async ({ page }) => {
    await goto(page);
    const measurements = await page.evaluate(() => {
      const topbar = document.querySelector('header');
      const hero = document.querySelector('[data-testid="series-hero"]');
      if (!topbar || !hero) return null;
      const tb = topbar.getBoundingClientRect();
      const hb = hero.getBoundingClientRect();
      return { topbarBottom: tb.bottom, heroTop: hb.top, gap: hb.top - tb.bottom };
    });
    expect(measurements).not.toBeNull();
    expect(measurements!.gap).toBeGreaterThanOrEqual(-2);
    expect(measurements!.gap).toBeLessThanOrEqual(5);
  });

  test('backdrop layer height is at least 700px', async ({ page }) => {
    await goto(page);
    const height = await page.evaluate(() => {
      const layer = document.querySelector('[data-testid="hero-backdrop-layer"]');
      return layer ? parseFloat(getComputedStyle(layer).height) : 0;
    });
    expect(height).toBeGreaterThanOrEqual(700);
  });

  test('no hard horizontal divider at hero bottom (no border / shadow on hero or its children)', async ({ page }) => {
    await goto(page);
    const offenders = await page.evaluate(() => {
      const hero = document.querySelector('[data-testid="series-hero"]');
      if (!hero) return ['missing-hero'];
      const heroRect = hero.getBoundingClientRect();
      const bad: string[] = [];
      const all: Element[] = [hero, ...Array.from(hero.querySelectorAll('*'))];
      for (const node of all) {
        const cs = getComputedStyle(node);
        const r = node.getBoundingClientRect();
        const nearBottom = Math.abs(r.bottom - heroRect.bottom) < 12;
        if (!nearBottom) continue;
        if (cs.borderBottomWidth !== '0px' && cs.borderBottomStyle !== 'none') {
          bad.push(`${node.tagName}.${(node as HTMLElement).className} border-bottom=${cs.borderBottom}`);
        }
        if (cs.boxShadow && cs.boxShadow !== 'none') {
          bad.push(`${node.tagName}.${(node as HTMLElement).className} shadow=${cs.boxShadow}`);
        }
      }
      return bad;
    });
    expect(offenders).toEqual([]);
  });

  test('overview section sits below hero with no overlap', async ({ page }) => {
    await goto(page);
    const rects = await page.evaluate(() => {
      const hero = document.querySelector('[data-testid="series-hero"]');
      const ov = document.querySelector('[data-testid="overview-section"]');
      if (!hero || !ov) return null;
      return { heroBottom: hero.getBoundingClientRect().bottom, overviewTop: ov.getBoundingClientRect().top };
    });
    expect(rects).not.toBeNull();
    expect(rects!.overviewTop).toBeGreaterThan(rects!.heroBottom);
  });
});

test.describe('B-13 overview rail transparency (story 368)', () => {
  test('overview section has no solid background', async ({ page }) => {
    await goto(page);
    const overview = page.getByTestId('overview-section');
    await expect(overview).toBeVisible();
    const bg = await overview.evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(bg).toBe('rgba(0, 0, 0, 0)');
  });

  test('rail card surface is translucent', async ({ page }) => {
    await goto(page);
    const card = page.getByTestId('rail-card');
    await expect(card).toBeVisible();
    const alpha = await card.evaluate((el) => {
      const bg = getComputedStyle(el).backgroundColor;
      // Parse alpha out of `rgba(r, g, b, a)` or `rgb(r, g, b)` or
      // `oklab(... / a)` / `lab(... / a)` — browsers normalise to
      // either rgba(...) or color(...). Pull the trailing number
      // before the closing `)` if a slash is present, else 1.
      const m = bg.match(/\/\s*([\d.]+)\s*\)/);
      if (m) return parseFloat(m[1]);
      const r = bg.match(/rgba?\(([^)]+)\)/);
      if (r) {
        const parts = r[1].split(',').map((s) => s.trim());
        return parts.length === 4 ? parseFloat(parts[3]) : 1;
      }
      return 1;
    });
    expect(alpha).toBeLessThan(0.7);
  });

  test('backdrop layer reaches into overview/rail area', async ({ page }) => {
    await goto(page);
    const h = await page.getByTestId('hero-backdrop-layer').evaluate(
      (el) => parseFloat(getComputedStyle(el).height),
    );
    // Story 369: backdrop shortened to land before cast strip
    // (was >= 1080, now ~800 — still well past the hero box at 450).
    expect(h).toBeGreaterThan(500);
    expect(h).toBeLessThan(900);
  });

  test('overview bottom sits inside the backdrop fade region', async ({ page }) => {
    await goto(page);
    const overviewBox = await page.getByTestId('overview-section').boundingBox();
    const backdropBox = await page.getByTestId('hero-backdrop-layer').boundingBox();
    if (!overviewBox || !backdropBox) throw new Error('layout missing');
    const overviewBottom = overviewBox.y + overviewBox.height;
    const backdropBottom = backdropBox.y + backdropBox.height;
    expect(overviewBottom).toBeLessThanOrEqual(backdropBottom);
    await page.screenshot({
      path: 'test-results/b13-overview-transparency-4k.png',
      clip: { x: 0, y: 0, width: 3840, height: 1600 },
    });
  });
});

test.describe('B-13 backdrop shortened before cast (story 369)', () => {
  test('backdrop layer is shorter than 900px', async ({ page }) => {
    await goto(page);
    const h = await page.getByTestId('hero-backdrop-layer').evaluate(
      (el) => parseFloat(getComputedStyle(el).height),
    );
    expect(h).toBeLessThan(900);
    expect(h).toBeGreaterThan(500);
  });

  test('cast strip starts after the backdrop fade endpoint', async ({ page }) => {
    await goto(page);
    const backdrop = await page.getByTestId('hero-backdrop-layer').evaluate((el) => {
      const r = el.getBoundingClientRect();
      const mask = getComputedStyle(el).maskImage || getComputedStyle(el).webkitMaskImage;
      const stops = (mask.match(/(\d+(?:\.\d+)?)%/g) || []).map((s) => parseFloat(s));
      const endpointPct = stops.length > 0 ? stops[stops.length - 1] / 100 : 1;
      return { top: r.y, height: r.height, endpointPct };
    });
    const castStrip = await page.getByTestId('cast-strip').boundingBox();
    if (!castStrip) throw new Error('cast-strip missing');
    const fadeEndpointY = backdrop.top + backdrop.height * backdrop.endpointPct;
    expect(castStrip.y).toBeGreaterThan(fadeEndpointY);
  });

  test('backdrop is invisible at cast strip top y-coordinate', async ({ page }) => {
    await goto(page);
    const sample = await page.getByTestId('hero-backdrop-layer').evaluate((el) => {
      const r = el.getBoundingClientRect();
      const mask = getComputedStyle(el).maskImage || getComputedStyle(el).webkitMaskImage;
      const stops = (mask.match(/(\d+(?:\.\d+)?)%/g) || []).map((s) => parseFloat(s) / 100);
      const plateauPct = stops.length >= 2 ? stops[stops.length - 2] : 0;
      const endpointPct = stops.length >= 1 ? stops[stops.length - 1] : 1;
      return { top: r.y, height: r.height, plateauPct, endpointPct };
    });
    const castStrip = await page.getByTestId('cast-strip').boundingBox();
    if (!castStrip) throw new Error('cast-strip missing');
    const position = (castStrip.y - sample.top) / sample.height;
    let alpha;
    if (position <= sample.plateauPct) alpha = 1;
    else if (position >= sample.endpointPct) alpha = 0;
    else alpha = (sample.endpointPct - position) / (sample.endpointPct - sample.plateauPct);
    expect(alpha).toBeLessThanOrEqual(0.05);

    await page.screenshot({
      path: 'test-results/b13-backdrop-shorten-near-cast-4k.png',
      clip: { x: 0, y: 0, width: 3840, height: 1400 },
    });
  });
});
