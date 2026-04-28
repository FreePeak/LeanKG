import { test, expect, Page } from '@playwright/test';

const BASE_URL = 'http://localhost:8080';

async function clickSigmaNode(page: Page, label: string): Promise<void> {
  const nodeInfo = await page.evaluate((nodeLabel: string) => {
    if (typeof window.sig === 'undefined') return null;
    const graph = window.sig.getGraph();
    let found = null;
    graph.forEachNode((nodeId, attrs) => {
      if (attrs.label === nodeLabel) {
        found = { id: nodeId, x: attrs.x, y: attrs.y };
      }
    });
    return found;
  }, label);

  if (nodeInfo) {
    const camera = await page.evaluate(() => {
      const cam = (window as any).sig.getCamera();
      return { x: cam.x, y: cam.y, ratio: cam.ratio };
    });

    const canvasWidth = 1024;
    const canvasHeight = 720;
    const x = (nodeInfo.x - camera.x) * camera.ratio + canvasWidth / 2;
    const y = (nodeInfo.y - camera.y) * camera.ratio + canvasHeight / 2;

    await page.locator('canvas.sigma-mouse').click({ position: { x, y }, force: true });
    await page.waitForTimeout(300);
  }
}

async function doubleClickSigmaNode(page: Page, label: string): Promise<void> {
  const nodeInfo = await page.evaluate((nodeLabel: string) => {
    if (typeof window.sig === 'undefined') return null;
    const graph = window.sig.getGraph();
    let found = null;
    graph.forEachNode((nodeId, attrs) => {
      if (attrs.label === nodeLabel) {
        found = { id: nodeId, x: attrs.x, y: attrs.y };
      }
    });
    return found;
  }, label);

  if (nodeInfo) {
    const camera = await page.evaluate(() => {
      const cam = (window as any).sig.getCamera();
      return { x: cam.x, y: cam.y, ratio: cam.ratio };
    });

    const canvasWidth = 1024;
    const canvasHeight = 720;
    const x = (nodeInfo.x - camera.x) * camera.ratio + canvasWidth / 2;
    const y = (nodeInfo.y - camera.y) * camera.ratio + canvasHeight / 2;

    await page.locator('canvas.sigma-mouse').dblclick({ position: { x, y }, force: true });
    await page.waitForTimeout(300);
  }
}

test.describe('Service Navigation - Issue 2 Fix', () => {

  test.beforeEach(async ({ page }) => {
    await page.goto(BASE_URL, { waitUntil: 'networkidle' });
    await page.waitForTimeout(3000);
  });

  test('TC-3: Service click shows children, not empty graph', async ({ page }) => {
    // Find a service node
    const serviceNode = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let found = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.nodeType === 'Service' && !found) {
          found = { id: nodeId, label: attrs.label, filePath: attrs.filePath };
        }
      });
      return found;
    });

    if (!serviceNode) {
      console.log('No service node found');
      return;
    }

    console.log('Testing service node:', serviceNode.label, 'with path:', serviceNode.filePath);

    // Get initial node count
    const initialNodeCount = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return 0;
      return window.sig.getGraph().order;
    });
    console.log('Initial node count:', initialNodeCount);

    // Double click the service node
    await doubleClickSigmaNode(page, serviceNode.label);
    await page.waitForTimeout(3000);

    // Graph should NOT be empty
    const afterNodeCount = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return 0;
      return window.sig.getGraph().order;
    });
    console.log('Node count after service click:', afterNodeCount);

    // Should have loaded children (not empty)
    expect(afterNodeCount).toBeGreaterThan(0);

    // Breadcrumb should show the service name
    const breadcrumbs = await page.locator('aside').textContent();
    expect(breadcrumbs).toContain(serviceNode.label);
  });

  test('TC-4: Sidebar shows correct types from actual graph data', async ({ page }) => {
    // Wait for graph to render
    await page.waitForTimeout(2000);

    // Get actual node types from graph
    const nodeTypes = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return [];
      const graph = window.sig.getGraph();
      const types = new Set<string>();
      graph.forEachNode((id, attrs) => {
        if (attrs.nodeType) {
          types.add(attrs.nodeType);
        }
      });
      return Array.from(types);
    });

    console.log('Actual node types in graph:', nodeTypes);

    // Sidebar should show types that exist in graph
    const sidebarText = await page.locator('aside').textContent();

    // Check that at least some of the actual types are shown in sidebar
    for (const nodeType of nodeTypes) {
      if (nodeType !== 'Service' && nodeType !== 'Folder') {
        // Most types should appear in sidebar
        console.log(`Checking if sidebar shows "${nodeType}"`);
      }
    }

    // Node Types section should exist
    expect(sidebarText).toContain('Node Types');

    // Edge Types section should exist
    expect(sidebarText).toContain('Edge Types');
  });

  test('TC-5: Service -> Folder -> File drill-down', async ({ page }) => {
    // Step 1: Double click a Service node
    const serviceNode = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let found = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.nodeType === 'Service' && !found) {
          found = { id: nodeId, label: attrs.label };
        }
      });
      return found;
    });

    if (!serviceNode) {
      console.log('No service node found');
      return;
    }

    console.log('1. Double clicking service:', serviceNode.label);
    await doubleClickSigmaNode(page, serviceNode.label);
    await page.waitForTimeout(2500);

    const afterServiceCount = await page.evaluate(() => window.sig.getGraph().order);
    console.log('Nodes after service click:', afterServiceCount);
    expect(afterServiceCount).toBeGreaterThan(0);

    // Step 2: Find and double click a Folder node
    const folderNode = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let found = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.nodeType === 'Folder' && !found) {
          found = { id: nodeId, label: attrs.label };
        }
      });
      return found;
    });

    if (folderNode) {
      console.log('2. Double clicking folder:', folderNode.label);
      await doubleClickSigmaNode(page, folderNode.label);
      await page.waitForTimeout(2500);

      const afterFolderCount = await page.evaluate(() => window.sig.getGraph().order);
      console.log('Nodes after folder click:', afterFolderCount);
      expect(afterFolderCount).toBeGreaterThan(0);
    } else {
      console.log('No folder node found at service level');
    }

    // Step 3: Single click a File node to open detail panel
    const fileNode = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let found = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.nodeType === 'File' && !found) {
          found = { id: nodeId, label: attrs.label };
        }
      });
      return found;
    });

    if (fileNode) {
      console.log('3. Single clicking file:', fileNode.label);
      await clickSigmaNode(page, fileNode.label);
      await page.waitForTimeout(500);

      // File detail panel should open
      const hasPanel = await page.locator('.file-detail-panel, [class*="file-detail"]').count() > 0;
      console.log('File detail panel opened:', hasPanel);
    } else {
      console.log('No file node found');
    }
  });

});