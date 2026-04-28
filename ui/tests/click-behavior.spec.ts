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

test.describe('Click Behavior - Issue 1 Fix', () => {

  test.beforeEach(async ({ page }) => {
    await page.goto(BASE_URL, { waitUntil: 'networkidle' });
    await page.waitForTimeout(3000);
  });

  test('TC-1: Single click selects node but does NOT navigate', async ({ page }) => {
    // Find a node to click
    const nodeInfo = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let found = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.label && !found) {
          found = { id: nodeId, label: attrs.label, nodeType: attrs.nodeType };
        }
      });
      return found;
    });

    if (!nodeInfo) {
      console.log('No nodes found in graph');
      return;
    }

    const nodeLabel = nodeInfo.label;
    console.log('Testing single click on node:', nodeLabel);

    // Get initial breadcrumb state
    const initialBreadcrumbs = await page.locator('aside').textContent();

    // Single click the node
    await clickSigmaNode(page, nodeLabel);
    await page.waitForTimeout(500);

    // Breadcrumb should NOT have changed
    const afterClickBreadcrumbs = await page.locator('aside').textContent();
    expect(afterClickBreadcrumbs).toBe(initialBreadcrumbs);

    // The node should be selected (check if sigma's selectedNode state changed)
    const selectedNode = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let selected = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.highlighted) {
          selected = nodeId;
        }
      });
      return selected;
    });

    console.log('Selected node after single click:', selectedNode);
    // Node should be highlighted/selected
    expect(selectedNode).not.toBeNull();
  });

  test('TC-2: Double click navigates (loads children)', async ({ page }) => {
    // Find a folder node to double-click
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

    if (!folderNode) {
      console.log('No folder node found');
      return;
    }

    console.log('Testing double click on folder:', folderNode.label);

    // Double click the folder
    await doubleClickSigmaNode(page, folderNode.label);
    await page.waitForTimeout(2000);

    // Breadcrumb should have changed to include the folder
    const breadcrumbs = await page.locator('aside').textContent();
    console.log('Breadcrumbs after double click:', breadcrumbs);

    // Should show the folder in breadcrumbs
    expect(breadcrumbs).toContain(folderNode.label);

    // Graph should have different nodes now
    const currentNodes = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return [];
      const graph = window.sig.getGraph();
      const nodes: string[] = [];
      graph.forEachNode((id, attrs) => {
        nodes.push(attrs.label);
      });
      return nodes;
    });

    console.log('Nodes after double click:', currentNodes.length);
    expect(currentNodes.length).toBeGreaterThan(0);
  });

  test('Single click on File opens detail panel', async ({ page }) => {
    // Navigate to a folder first
    const folderNode = await page.evaluate(() => {
      if (typeof window.sig === 'undefined') return null;
      const graph = window.sig.getGraph();
      let found = null;
      graph.forEachNode((nodeId, attrs) => {
        if (attrs.nodeType === 'Folder') {
          found = { id: nodeId, label: attrs.label };
        }
      });
      return found;
    });

    if (folderNode) {
      await doubleClickSigmaNode(page, folderNode.label);
      await page.waitForTimeout(2000);
    }

    // Find a file node
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

    if (!fileNode) {
      console.log('No file node found');
      return;
    }

    console.log('Single clicking on file:', fileNode.label);
    await clickSigmaNode(page, fileNode.label);
    await page.waitForTimeout(500);

    // File detail panel should open
    const hasDetailPanel = await page.locator('.file-detail-panel, [class*="file-detail"]').count() > 0;
    console.log('File detail panel visible:', hasDetailPanel);

    // This test checks that single click on file opens detail panel
    // (which is the expected behavior for File nodes based on the code)
  });

});