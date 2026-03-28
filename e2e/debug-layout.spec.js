const { chromium } = require('playwright');

(async () => {
    const browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    
    const errors = [];
    const logs = [];
    page.on('console', msg => {
        logs.push(`[${msg.type()}] ${msg.text()}`);
        if (msg.type() === 'error') errors.push(msg.text());
    });
    page.on('pageerror', err => errors.push(err.message));
    
    console.log('Navigating to graph page...');
    await page.goto('http://localhost:8080/graph', { waitUntil: 'networkidle', timeout: 60000 });
    await page.waitForTimeout(3000);
    
    console.log('\n--- Checking initial state ---');
    
    const initialState = await page.evaluate(async () => {
        const sigmaExists = typeof sigma !== 'undefined';
        const sigmaVersion = sigmaExists ? sigma.version : 'N/A';
        
        const container = document.getElementById('graph-container');
        const canvases = container.querySelectorAll('canvas');
        
        // Check if sig instance exists
        let sigInstance = null;
        let graphNodes = 0;
        let graphEdges = 0;
        
        return {
            sigmaExists,
            sigmaVersion,
            canvasCount: canvases.length,
            containerContent: container.innerHTML.substring(0, 200)
        };
    });
    
    console.log('Initial state:', JSON.stringify(initialState, null, 2));
    
    console.log('\n--- Testing Run Layout button ---');
    await page.click('button:has-text("Run Layout")');
    await page.waitForTimeout(3000);
    
    const afterLayout = await page.evaluate(async () => {
        let sigInstance = null;
        let graphNodes = 0;
        try {
            // Sigma v1 stores instance in window
            sigInstance = window.sig;
            if (sigInstance) {
                graphNodes = sigInstance.graph ? sigInstance.graph.nodes().length : 'no graph';
            }
        } catch(e) {
            return { error: e.message };
        }
        return { sigExists: !!sigInstance, graphNodes };
    });
    
    console.log('After layout click:', JSON.stringify(afterLayout, null, 2));
    
    console.log('\n--- Checking all console logs ---');
    logs.forEach(l => console.log(l));
    
    console.log('\n--- Errors ---');
    errors.forEach(e => console.log('ERROR:', e));
    
    await browser.close();
})();
