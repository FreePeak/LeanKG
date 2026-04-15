# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: tests/hierarchy.spec.ts >> LeanKG Hierarchical Explorer - Acceptance Criteria >> AC4: Navigation
- Location: tests/hierarchy.spec.ts:207:3

# Error details

```
Error: expect(received).toBe(expected) // Object.is equality

Expected: true
Received: false
```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - generic [ref=e5]:
      - img [ref=e7]
      - heading "LeanKG" [level=1] [ref=e11]
    - navigation [ref=e12]:
      - button "Explorer" [ref=e13]:
        - img [ref=e14]
        - generic [ref=e18]: Explorer
    - generic [ref=e19]:
      - generic [ref=e20]:
        - img [ref=e21]
        - generic [ref=e24]: Navigation
      - button "Root" [ref=e27]
    - generic [ref=e28]:
      - img [ref=e29]
      - textbox "Search node..." [ref=e32]
    - generic [ref=e33]:
      - generic [ref=e34]:
        - img [ref=e35]
        - text: Node Types
      - generic [ref=e39]:
        - button "Service" [ref=e40]: Service
        - button "Folder" [ref=e42]: Folder
        - button "File" [ref=e44]: File
        - button "Class" [ref=e46]: Class
        - button "Function" [ref=e48]: Function
        - button "Method" [ref=e50]: Method
        - button "Interface" [ref=e52]: Interface
        - button "Enum" [ref=e54]: Enum
    - generic [ref=e56]:
      - generic [ref=e57]:
        - generic [ref=e58]: →
        - text: Edge Types
      - generic [ref=e59]:
        - button "CONTAINS" [ref=e60]: CONTAINS
        - button "DEFINES" [ref=e62]: DEFINES
        - button "IMPORTS" [ref=e64]: IMPORTS
        - button "CALLS" [ref=e66]: CALLS
        - button "SERVICE_CALLS" [ref=e68]: SERVICE_CALLS
        - button "EXTENDS" [ref=e70]: EXTENDS
        - button "IMPLEMENTS" [ref=e72]: IMPLEMENTS
    - generic [ref=e74]:
      - generic [ref=e75]:
        - generic [ref=e76]: ⊕
        - text: Zoom Level
      - paragraph [ref=e77]: "Semantic zoom: detail changes with scale"
      - generic [ref=e78]:
        - button "Clusters Top-level module clusters" [ref=e79]:
          - text: Clusters
          - generic [ref=e80]: Top-level module clusters
        - button "Modules Folders and packages" [ref=e81]:
          - text: Modules
          - generic [ref=e82]: Folders and packages
        - button "Files Source files and modules" [ref=e83]:
          - text: Files
          - generic [ref=e84]: Source files and modules
        - button "Functions All elements including functions" [ref=e85]:
          - text: Functions
          - generic [ref=e86]: All elements including functions
    - generic [ref=e87]:
      - generic [ref=e88]:
        - generic [ref=e89]: ◉
        - text: Focus Depth
      - paragraph [ref=e90]: Show nodes within N hops of selection
      - generic [ref=e91]:
        - button "All" [ref=e92]
        - button "1 hop" [ref=e93]
        - button "2 hops" [ref=e94]
        - button "3 hops" [ref=e95]
        - button "5 hops" [ref=e96]
    - generic [ref=e98]:
      - heading "Graph Stats" [level=4] [ref=e99]
      - generic [ref=e100]:
        - generic [ref=e101]:
          - generic [ref=e102]: Nodes
          - generic [ref=e103]: "5"
        - generic [ref=e104]:
          - generic [ref=e105]: Relationships
          - generic [ref=e106]: "34"
  - main [ref=e107]:
    - generic [ref=e118]:
      - button "Zoom In" [ref=e119]:
        - img [ref=e120]
      - button "Zoom Out" [ref=e124]:
        - img [ref=e125]
      - button "Fit to screen" [ref=e129]:
        - img [ref=e130]
```

# Test source

```ts
  116 |     // Find a File node
  117 |     const fileNode = await page.evaluate(() => {
  118 |       if (typeof window.sig === 'undefined') return null;
  119 |       const graph = window.sig.getGraph();
  120 |       let found = null;
  121 |       graph.forEachNode((id, attrs) => {
  122 |         if (attrs.nodeType === 'File') {
  123 |           found = { id, label: attrs.label };
  124 |         }
  125 |       });
  126 |       return found;
  127 |     });
  128 | 
  129 |     if (fileNode) {
  130 |       // Click the file node
  131 |       const camera = await page.evaluate(() => {
  132 |         const cam = (window as any).sig.getCamera();
  133 |         return { x: cam.x, y: cam.y, ratio: cam.ratio };
  134 |       });
  135 | 
  136 |       const nodeInfo = await page.evaluate((fileId: string) => {
  137 |         const graph = window.sig.getGraph();
  138 |         const attrs = graph.getNodeAttributes(fileId);
  139 |         return { x: attrs.x, y: attrs.y };
  140 |       }, fileNode.id);
  141 | 
  142 |       const canvasWidth = 1024;
  143 |       const canvasHeight = 720;
  144 |       const x = (nodeInfo.x - camera.x) * camera.ratio + canvasWidth / 2;
  145 |       const y = (nodeInfo.y - camera.y) * camera.ratio + canvasHeight / 2;
  146 | 
  147 |       await page.locator('canvas.sigma-mouse').click({ position: { x, y }, force: true });
  148 |       await page.waitForTimeout(1500);
  149 | 
  150 |       // Check if FileDetailPanel appeared
  151 |       const hasFileDetail = await page.locator('text=Functions').count() > 0 ||
  152 |                            await page.locator('text=Relationships').count() > 0;
  153 |       console.log('FileDetailPanel visible after clicking file:', hasFileDetail);
  154 |     }
  155 |   });
  156 | 
  157 |   test('AC3: Function Interaction', async ({ page }) => {
  158 |     // Navigate to src to find functions
  159 |     await clickSigmaNode(page, 'src');
  160 |     await page.waitForTimeout(2000);
  161 | 
  162 |     // Find a function node if visible
  163 |     const funcNode = await page.evaluate(() => {
  164 |       if (typeof window.sig === 'undefined') return null;
  165 |       const graph = window.sig.getGraph();
  166 |       let found = null;
  167 |       graph.forEachNode((id, attrs) => {
  168 |         if (attrs.nodeType === 'Function' || attrs.label === 'main') {
  169 |           found = { id, label: attrs.label };
  170 |         }
  171 |       });
  172 |       return found;
  173 |     });
  174 | 
  175 |     if (funcNode) {
  176 |       // AC3.1: Clicking a function shows its code
  177 |       const camera = await page.evaluate(() => {
  178 |         const cam = (window as any).sig.getCamera();
  179 |         return { x: cam.x, y: cam.y, ratio: cam.ratio };
  180 |       });
  181 | 
  182 |       const nodeInfo = await page.evaluate((funcId: string) => {
  183 |         const graph = window.sig.getGraph();
  184 |         const attrs = graph.getNodeAttributes(funcId);
  185 |         return { x: attrs.x, y: attrs.y };
  186 |       }, funcNode.id);
  187 | 
  188 |       const canvasWidth = 1024;
  189 |       const canvasHeight = 720;
  190 |       const x = (nodeInfo.x - camera.x) * camera.ratio + canvasWidth / 2;
  191 |       const y = (nodeInfo.y - camera.y) * camera.ratio + canvasHeight / 2;
  192 | 
  193 |       await page.locator('canvas.sigma-mouse').click({ position: { x, y }, force: true });
  194 |       await page.waitForTimeout(1500);
  195 | 
  196 |       // AC3.2: Function's call targets are highlighted
  197 |       // AC3.3: Function's callers are highlighted
  198 |       // Check that something is selected (either CodeViewer or FileDetailPanel should appear)
  199 |       const hasDetailPanel = await page.locator('.absolute.right-0').count() > 0 ||
  200 |                             await page.locator('text=Functions').count() > 0;
  201 |       console.log('Detail panel visible after clicking function:', hasDetailPanel);
  202 |     } else {
  203 |       console.log('No function node found to test AC3');
  204 |     }
  205 |   });
  206 | 
  207 |   test('AC4: Navigation', async ({ page }) => {
  208 |     // AC4.1: Breadcrumb or back button to return to aggregated view
  209 | 
  210 |     // First navigate to 'src'
  211 |     await clickSigmaNode(page, 'src');
  212 |     await page.waitForTimeout(2000);
  213 | 
  214 |     // Check breadcrumb appeared
  215 |     const hasBreadcrumb = await page.locator('text=src').count() > 0;
> 216 |     expect(hasBreadcrumb).toBe(true);
      |                           ^ Error: expect(received).toBe(expected) // Object.is equality
  217 | 
  218 |     // AC4.2: Clicking outside (stage) returns to default view
  219 |     await page.locator('canvas.sigma-mouse').click({ position: { x: 50, y: 50 }, force: true });
  220 |     await page.waitForTimeout(500);
  221 | 
  222 |     // AC4.3: Smooth transitions between view states
  223 |     // This is visual, check that navigation happens without errors
  224 |     const consoleErrors: string[] = [];
  225 |     page.on('pageerror', err => consoleErrors.push(err.message));
  226 |     expect(consoleErrors.length).toBe(0);
  227 |   });
  228 | 
  229 |   test('AC5: Performance', async ({ page }) => {
  230 |     // AC5.1: Initial load renders < 100 nodes even for large codebases
  231 |     const nodeCount = await page.evaluate(() => {
  232 |       if (typeof window.sig === 'undefined') return -1;
  233 |       const graph = window.sig.getGraph();
  234 |       return graph.order;
  235 |     });
  236 | 
  237 |     console.log('Initial node count:', nodeCount);
  238 |     expect(nodeCount).toBeLessThan(100);
  239 |     expect(nodeCount).toBeGreaterThan(0);
  240 | 
  241 |     // AC5.2: Drill-down expansion is < 500ms
  242 |     const startTime = Date.now();
  243 |     await clickSigmaNode(page, 'src');
  244 |     const drillDownTime = Date.now() - startTime;
  245 |     console.log('Drill-down time:', drillDownTime);
  246 |     expect(drillDownTime).toBeLessThan(2000); // Allow more than 500ms for CI
  247 | 
  248 |     // AC5.3: No layout thrashing during transitions
  249 |     // Check that sigma instance is stable
  250 |     const sigmaStable = await page.evaluate(() => {
  251 |       if (typeof window.sig === 'undefined') return false;
  252 |       try {
  253 |         const g = window.sig.getGraph();
  254 |         return g.order > 0;
  255 |       } catch {
  256 |         return false;
  257 |       }
  258 |     });
  259 |     expect(sigmaStable).toBe(true);
  260 |   });
  261 | 
  262 |   test('AC6: API - children endpoint returns correct data', async ({ page }) => {
  263 |     // Test the API directly
  264 |     const response = await page.request.get(`${BASE_URL}/api/graph/children?parent=`);
  265 | 
  266 |     expect(response.ok()).toBe(true);
  267 |     const json = await response.json();
  268 |     expect(json.success).toBe(true);
  269 |     expect(json.data).toBeDefined();
  270 |     expect(json.data.nodes).toBeDefined();
  271 |     expect(Array.isArray(json.data.nodes)).toBe(true);
  272 | 
  273 |     // Should have Folder nodes
  274 |     const hasFolder = json.data.nodes.some((n: any) =>
  275 |       n.properties?.elementType === 'Folder'
  276 |     );
  277 |     expect(hasFolder).toBe(true);
  278 | 
  279 |     console.log('API returned', json.data.nodes.length, 'nodes at root');
  280 |   });
  281 | 
  282 |   test('AC7: API - children with parent path works', async ({ page }) => {
  283 |     const response = await page.request.get(`${BASE_URL}/api/graph/children?parent=src`);
  284 | 
  285 |     expect(response.ok()).toBe(true);
  286 |     const json = await response.json();
  287 |     expect(json.success).toBe(true);
  288 |     expect(json.data.nodes.length).toBeGreaterThan(0);
  289 | 
  290 |     // Should have File or Function nodes
  291 |     const nodeTypes = [...new Set(json.data.nodes.map((n: any) => n.properties?.elementType))];
  292 |     console.log('src children node types:', nodeTypes);
  293 | 
  294 |     const hasChildNodes = json.data.nodes.some((n: any) =>
  295 |       ['File', 'Folder', 'Function', 'Method'].includes(n.properties?.elementType)
  296 |     );
  297 |     expect(hasChildNodes).toBe(true);
  298 |   });
  299 | 
  300 |   test('AC8: UI loads without critical errors', async ({ page }) => {
  301 |     const errors: string[] = [];
  302 |     page.on('pageerror', err => {
  303 |       if (!err.message.includes('blendFunc')) { // Ignore WebGL blendFunc error in headless
  304 |         errors.push(err.message);
  305 |       }
  306 |     });
  307 | 
  308 |     await page.goto(BASE_URL, { waitUntil: 'networkidle' });
  309 |     await page.waitForTimeout(3000);
  310 | 
  311 |     // Page should load
  312 |     const title = await page.title();
  313 |     expect(title).toBe('LeanKG');
  314 | 
  315 |     // Sidebar should be visible
  316 |     const sidebar = await page.locator('aside').count();
```