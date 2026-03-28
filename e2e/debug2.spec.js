const { chromium } = require('playwright');

(async () => {
    const browser = await chromium.launch({ headless: false });
    const page = await browser.newPage();
    
    const errors = [];
    page.on('console', msg => {
        if (msg.type() === 'error') errors.push(msg.text());
    });
    page.on('pageerror', err => errors.push(err.message));
    
    console.log('Navigating to graph page...');
    await page.goto('http://localhost:8080/graph', { waitUntil: 'networkidle', timeout: 60000 });
    await page.waitForTimeout(5000);
    
    console.log('\n--- Checking graph ---');
    const graphState = await page.evaluate(async () => {
        const sig = window.sig;
        if (!sig) return { error: 'No sig instance' };
        
        const graph = sig.graph;
        if (!graph) return { error: 'No graph' };
        
        const nodes = graph.nodes ? graph.nodes() : [];
        const edges = graph.edges ? graph.edges() : [];
        
        return {
            sigmaVersion: sig.version,
            nodeCount: nodes.length,
            edgeCount: edges.length
        };
    });
    
    console.log('Graph state:', JSON.stringify(graphState, null, 2));
    
    if (errors.length > 0) {
        console.log('\nErrors:', errors);
    }
    
    await browser.close();
    
    if (graphState.nodeCount > 0 && errors.length === 0) {
        console.log('\nTest: PASS');
        process.exit(0);
    } else {
        console.log('\nTest: FAIL');
        process.exit(1);
    }
})();
