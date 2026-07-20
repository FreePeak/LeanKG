import { chromium } from '@playwright/test';
import { spawn } from 'child_process';
import { fileURLToPath } from 'url';
import path from 'path';

const UI_ROOT = path.resolve(fileURLToPath(new URL('..', import.meta.url)));
const OUT = path.resolve(UI_ROOT, '../docs/reports/screenshots');
const PORT = 5203;
const BASE = `http://127.0.0.1:${PORT}`;

const vite = spawn(
  'npx',
  ['vite', '--host', '127.0.0.1', '--port', String(PORT), '--strictPort'],
  { cwd: UI_ROOT, stdio: ['ignore', 'pipe', 'pipe'] },
);

async function waitReady(url, tries = 80) {
  for (let i = 0; i < tries; i++) {
    try {
      const r = await fetch(url);
      if (r.ok) return;
    } catch {
      /* retry */
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error('vite down');
}

async function waitNodes(page) {
  await page.waitForFunction(() => {
    const t = document.querySelector('[data-testid="status-bar"]')?.textContent || '';
    const m = t.match(/nodes:\s*(\d+)/);
    return m && Number(m[1]) > 5;
  }, null, { timeout: 120_000 });
}

try {
  await waitReady(`${BASE}/`);
  // Warm expand via proxy
  for (let i = 0; i < 20; i++) {
    try {
      const r = await fetch(`${BASE}/api/graph/expand-service?path=src/cli&all=true`);
      if (r.ok) {
        const j = await r.json();
        console.log('proxy nodes', j?.data?.nodes?.length ?? 0);
        if ((j?.data?.nodes?.length ?? 0) > 5) break;
      }
    } catch {
      /* retry */
    }
    await new Promise((r) => setTimeout(r, 300));
  }

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });

  await page.goto(`${BASE}/?path=src/cli`, { waitUntil: 'domcontentloaded' });
  await waitNodes(page);
  await page.waitForTimeout(4500);
  await page.screenshot({ path: `${OUT}/01-force-src.png` });
  await page.screenshot({ path: `${OUT}/09-force-full-viewport.png` });

  await page.getByTestId('layout-tree').click();
  await page.waitForTimeout(2500);
  await page.screenshot({ path: `${OUT}/02-tree-src.png` });
  await page.screenshot({ path: `${OUT}/08-tree-full-viewport.png` });

  await page.getByTestId('layout-circles').click();
  await page.waitForTimeout(2500);
  await page.screenshot({ path: `${OUT}/03-circles-src.png` });
  await page.screenshot({ path: `${OUT}/10-circles-full-viewport.png` });

  await page.getByTestId('layout-force').click();
  await page.waitForTimeout(1200);
  await page.getByTestId('query-fab').click();
  await page.waitForTimeout(800);
  await page.screenshot({ path: `${OUT}/04-query-panel.png` });

  await page.getByTestId('header-search').fill('cli');
  await page.getByTestId('header-search').press('Enter');
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${OUT}/05-search.png` });

  const fileBtn = page.locator('[data-testid="file-tree-list"] button').first();
  if (await fileBtn.count()) {
    await fileBtn.click({ force: true });
    await page.waitForTimeout(2500);
  }
  await page.screenshot({ path: `${OUT}/07-code-panel.png` });

  await page.goto(`${BASE}/?skipGraph=1`);
  await page.waitForSelector('[data-testid="mega-graph-banner"]', { timeout: 30_000 });
  await page.screenshot({ path: `${OUT}/06-mega-skip.png` });

  await browser.close();
  console.log('screenshots done', OUT);
} finally {
  vite.kill('SIGTERM');
}
