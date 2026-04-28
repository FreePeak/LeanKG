const { test, expect } = require('@playwright/test');

test.describe('LeanKG Web UI/UX Reconstruction', () => {

    test.beforeEach(async ({ page }) => {
        page.on('console', msg => {
            if (msg.type() === 'error') {
               console.log('PAGE ERROR LOG:', msg.text());
            }
        });

        // Mock the API response to simulate a populated graph
        await page.route('**/api/graph/data', async route => {
            await route.fulfill({
                json: {
                    success: true,
                    data: {
                        nodes: [
                            { id: '1', label: 'Main', properties: { elementType: 'Function', filePath: 'main.rs', startLine: 1, endLine: 10 } },
                            { id: '2', label: 'Helper', properties: { elementType: 'Class', filePath: 'helper.rs' } }
                        ],
                        relationships: [
                            { sourceId: '1', targetId: '2', type: 'CALLS' }
                        ]
                    }
                }
            });
        });

        await page.route('**/api/file**', async route => {
            await route.fulfill({
                json: {
                    success: true,
                    data: {
                        content: 'fn main() {\n  println!("Hello");\n}'
                    }
                }
            });
        });

        const port = process.env.PORT || 8080;
        await page.goto(`http://localhost:${port}`);
    });

    test('should display the main dashboard and specific UI elements', async ({ page }) => {
        await expect(page.locator('h1')).toContainText('LeanKG', { timeout: 10000 });
        await expect(page.locator('text=Explorer').first()).toBeVisible();
    });

    test('should load graph data properly and update the UI stats', async ({ page }) => {
        // Wait for stats to populate
        await expect(page.locator('text=Nodes')).toBeVisible({ timeout: 10000 });
        await expect(page.locator('text=Relationships')).toBeVisible({ timeout: 10000 });
        await expect(page.locator('.text-blue-400.font-bold')).toHaveText('2');
        await expect(page.locator('.text-amber-400.font-bold')).toHaveText('1');
    });

    test('should render the WebGL canvas via Sigma', async ({ page }) => {
        const canvas = page.locator('canvas').first();
        await expect(canvas).toBeVisible({ timeout: 10000 });
        
        // Ensure web worker layout didn't crash sigma instance
        const hasSig = await page.evaluate(() => {
            return typeof window.sig !== 'undefined' && window.sig.getGraph().order === 2;
        });
        expect(hasSig).toBeTruthy();
    });

    test('should filter by edge types using the Sidebar filters', async ({ page }) => {
        const callsButton = page.locator('button', { hasText: 'CALLS' }).first();
        await callsButton.waitFor({ state: 'visible' });
        
        // Toggle the filter
        await callsButton.click();
        
        // Try toggling another that isn't present
        const importsButton = page.locator('button', { hasText: 'IMPORTS' }).first();
        await importsButton.click();

        // Edge count should not be visually present as an error
        await expect(page.locator('text=Edge Types')).toBeVisible();
    });

    test('should display CodeViewer when a node is selected', async ({ page }) => {
        // Wait for Sigma Graph instance to be registered bounds
        await page.waitForFunction(() => typeof window.sig !== 'undefined' && window.sig.getGraph().order > 0);

        // Simulate a node click via Sigma instance
        await page.evaluate(() => {
            window.sig.emit('clickNode', { node: '1' });
        });

        // The CodeViewer component should slide in and display "Selected"
        await expect(page.locator('text=Selected')).toBeVisible({ timeout: 5000 });
        await expect(page.locator('text=main.rs')).toBeVisible();

        // Mock syntax highlighting or source fetched
        await expect(page.locator('text=fn main()')).toBeVisible({ timeout: 5000 });
    });
});
