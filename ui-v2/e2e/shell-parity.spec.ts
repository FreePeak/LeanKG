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

  test('status bar present when connected', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(1500);
    await expect(page.getByTestId('status-bar')).toBeVisible();
  });
});
