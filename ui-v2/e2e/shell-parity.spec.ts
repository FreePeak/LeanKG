import { test, expect } from '@playwright/test';

const BACKEND_URL = process.env.BACKEND_URL ?? 'http://127.0.0.1:8080';
const FRONTEND_URL = process.env.FRONTEND_URL ?? 'http://127.0.0.1:5173';

async function serversAvailable(): Promise<boolean> {
  if (process.env.E2E === '1') return true;
  try {
    const [b, f] = await Promise.all([
      fetch(`${BACKEND_URL}/api/index/status`),
      fetch(FRONTEND_URL),
    ]);
    return b.ok && f.ok;
  } catch {
    return false;
  }
}

test.beforeAll(async () => {
  if (!(await serversAvailable())) {
    test.skip(true, 'leankg serve (:8080) or ui-v2 (:5173) not available — set E2E=1 to force');
  }
});

test.describe('UI v2 shell parity', () => {
  test('server-connect: reaches exploring or overview or onboarding', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(2000);
    // Header chrome always mounts once React boots; canvas may be covered by loading overlay.
    await expect(page.getByTestId('connection-status')).toBeVisible({ timeout: 30_000 });
    const exploring = page.getByTestId('graph-canvas');
    const onboarding = page.getByTestId('onboarding');
    const overview = page.getByTestId('mega-graph-banner');
    await expect(exploring.or(onboarding).or(overview).first()).toBeAttached({ timeout: 30_000 });
  });

  test('tree-view: layout mode toggles exist', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(1500);
    if (await page.getByTestId('onboarding').isVisible().catch(() => false)) {
      test.skip(true, 'backend not connected');
    }
    await expect(page.getByTestId('layout-force')).toBeVisible();
    await expect(page.getByTestId('layout-tree')).toBeVisible();
    await expect(page.getByTestId('layout-circles')).toBeVisible();
    await page.getByTestId('layout-tree').click();
    await page.getByTestId('layout-circles').click();
    await page.getByTestId('layout-force').click();
  });

  test('filters-and-filetree', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(1500);
    if (await page.getByTestId('onboarding').isVisible().catch(() => false)) {
      test.skip(true, 'backend not connected');
    }
    await expect(page.getByTestId('file-tree-panel')).toBeVisible();
    await expect(page.getByTestId('node-type-filters')).toBeVisible();
    await page.getByTestId('reset-filters').click();
  });

  test('search-and-query', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(1500);
    if (await page.getByTestId('onboarding').isVisible().catch(() => false)) {
      test.skip(true, 'backend not connected');
    }
    await page.getByTestId('header-search').fill('main');
    await page.getByTestId('header-search').press('Enter');
    await page.getByTestId('query-fab').click();
    await expect(page.getByTestId('query-panel')).toBeVisible();
  });

  test('mega-graph-skip via URL', async ({ page }) => {
    await page.goto('/?skipGraph=1');
    await page.waitForTimeout(2500);
    if (await page.getByTestId('onboarding').isVisible().catch(() => false)) {
      test.skip(true, 'backend not connected');
    }
    await expect(page.getByTestId('mega-graph-banner')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('load-graph-anyway')).toBeVisible();
  });

  test('graph canvas fills viewport height (not a thin strip)', async ({ page }) => {
    await page.goto('/?path=src/cli');
    await page.waitForFunction(() => {
      const t = document.querySelector('[data-testid="status-bar"]')?.textContent || '';
      const m = t.match(/nodes:\s*(\d+)/);
      return m && Number(m[1]) > 5;
    }, null, { timeout: 90_000 });
    await page.getByTestId('layout-tree').click();
    await page.waitForTimeout(1200);
    const metrics = await page.evaluate(() => {
      const canvas = document.querySelector('[data-testid="graph-canvas"]') as HTMLElement | null;
      const sigma = canvas?.querySelector('.sigma-container') as HTMLElement | null;
      if (!canvas || !sigma) return null;
      return {
        canvasH: canvas.clientHeight,
        sigmaH: sigma.clientHeight,
        canvasW: canvas.clientWidth,
      };
    });
    expect(metrics).not.toBeNull();
    expect(metrics!.canvasH).toBeGreaterThan(400);
    expect(metrics!.sigmaH).toBeGreaterThan(400);
    // Sigma container should track the canvas box (not collapse to ~1/3 height).
    expect(metrics!.sigmaH / metrics!.canvasH).toBeGreaterThan(0.9);
  });

  test('breadcrumbs mount after graph load', async ({ page }) => {
    await page.goto('/?path=src/cli');
    await page.waitForTimeout(2000);
    if (await page.getByTestId('onboarding').isVisible().catch(() => false)) {
      test.skip(true, 'backend not connected');
    }
    await expect(page.getByTestId('graph-breadcrumbs')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId('crumb-0')).toContainText('Overview');
  });
});
