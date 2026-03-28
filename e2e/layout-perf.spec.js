const { chromium } = require('playwright');

(async () => {
    const browser = await chromium.launch({ headless: false });
    const page = await browser.newPage();
    
    const errors = [];
    page.on('console', msg => {
        if (msg.type() === 'error') {
            const text = msg.text();
            if (!text.includes('404') && !text.includes('favicon')) {
                errors.push(text);
            }
        }
    });
    page.on('pageerror', err => errors.push(err.message));
    
    console.log('Navigating to graph page...');
    await page.goto('http://localhost:8080/graph', { waitUntil: 'networkidle', timeout: 60000 });
    await page.waitForTimeout(5000);
    
    console.log('\n--- Checking initial render ---');
    const initialState = await page.evaluate(async () => {
        const sig = window.sig;
        if (!sig) return { error: 'No sig instance' };
        
        const graph = sig.graph;
        if (!graph) return { error: 'No graph' };
        
        const nodes = graph.nodes();
        let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
        
        nodes.forEach(n => {
            const x = graph.getNodeAttribute(n, 'x');
            const y = graph.getNodeAttribute(n, 'y');
            minX = Math.min(minX, x);
            maxX = Math.max(maxX, x);
            minY = Math.min(minY, y);
            maxY = Math.max(maxY, y);
        });
        
        return {
            sigmaVersion: sig.version,
            nodeCount: nodes.length,
            edgeCount: graph.edges().length,
            spread: { x: maxX - minX, y: maxY - minY }
        };
    });
    
    console.log('Initial state:', JSON.stringify(initialState, null, 2));
    
    console.log('\n--- Clicking Run Layout ---');
    await page.click('button:has-text("Run Layout")');
    await page.waitForTimeout(3000);
    
    const afterLayout = await page.evaluate(async () => {
        const sig = window.sig;
        if (!sig) return { error: 'No sig instance' };
        
        const graph = sig.graph;
        if (!graph) return { error: 'No graph' };
        
        const nodes = graph.nodes();
        let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
        
        nodes.forEach(n => {
            const x = graph.getNodeAttribute(n, 'x');
            const y = graph.getNodeAttribute(n, 'y');
            minX = Math.min(minX, x);
            maxX = Math.max(maxX, x);
            minY = Math.min(minY, y);
            maxY = Math.max(maxY, y);
        });
        
        return {
            nodeCount: nodes.length,
            spread: { x: maxX - minX, y: maxY - minY }
        };
    });
    
    console.log('After layout:', JSON.stringify(afterLayout, null, 2));
    
    if (errors.length > 0) {
        console.log('\nCritical errors:', errors);
    }
    
    await browser.close();
    
    const isRendered = initialState.nodeCount > 0;
    const hasSpread = initialState.spread && (initialState.spread.x > 50 || initialState.spread.y > 50);
    
    console.log('\n--- Summary ---');
    console.log('Nodes rendered:', isRendered, '(' + (initialState.nodeCount || 0) + ')');
    console.log('Has spread:', hasSpread, '(x=' + (initialState.spread?.x || 0).toFixed(1) + ', y=' + (initialState.spread?.y || 0).toFixed(1) + ')');
    console.log('Critical errors:', errors.length);
    
    if (isRendered && hasSpread && errors.length === 0) {
        console.log('\nTest: PASS');
        process.exit(0);
    } else {
        console.log('\nTest: FAIL');
        process.exit(1);
    }
})();
