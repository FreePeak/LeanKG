const { chromium } = require('playwright');

(async () => {
    const browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    
    const errors = [];
    page.on('console', msg => {
        if (msg.type() === 'error') {
            errors.push(msg.text());
        }
    });
    page.on('pageerror', err => {
        errors.push('PageError: ' + err.message);
    });
    
    console.log('Navigating to graph page...');
    await page.goto('http://localhost:8080/graph', { waitUntil: 'networkidle', timeout: 60000 });
    
    console.log('Waiting for graph to render...');
    await page.waitForTimeout(5000);
    
    console.log('\n--- Console errors ---');
    errors.forEach(e => console.log('ERROR:', e));
    
    if (errors.length === 0) {
        console.log('No errors detected');
    }
    
    console.log('\n--- Checking graph container ---');
    const graphContainer = await page.$('#graph-container');
    if (graphContainer) {
        const innerHTML = await graphContainer.innerHTML();
        console.log('Content length:', innerHTML.length);
        if (innerHTML.length < 200) {
            console.log('Content:', innerHTML);
        }
    }
    
    console.log('\n--- Checking for canvas ---');
    const canvases = await page.$$('canvas');
    console.log('Canvas count:', canvases.length);
    
    await browser.close();
    
    if (canvases.length > 0) {
        console.log('\nGraph rendering: PASS');
        process.exit(0);
    } else {
        console.log('\nGraph rendering: FAIL');
        process.exit(1);
    }
})();
