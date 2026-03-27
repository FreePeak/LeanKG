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
        errors.push(err.message);
    });
    
    console.log('Navigating to graph page...');
    await page.goto('http://localhost:8080/graph', { waitUntil: 'networkidle', timeout: 30000 });
    
    console.log('Waiting for graph to render...');
    await page.waitForTimeout(3000);
    
    console.log('Checking for sigma.js canvas...');
    const canvas = await page.$('canvas');
    
    if (canvas) {
        console.log('SUCCESS: Sigma.js canvas found!');
    } else {
        console.log('ERROR: No canvas element found - sigma.js may not have rendered');
    }
    
    const graphContainer = await page.$('#graph-container');
    if (graphContainer) {
        const innerHTML = await graphContainer.innerHTML();
        console.log('Graph container content length:', innerHTML.length);
        if (innerHTML.includes('sigma')) {
            console.log('SUCCESS: Sigma.js appears to be present');
        }
    }
    
    if (errors.length > 0) {
        console.log('Console errors found:');
        errors.forEach(e => console.log('  - ' + e));
    } else {
        console.log('No console errors detected');
    }
    
    await browser.close();
    
    if (canvas) {
        console.log('\nGraph rendering: PASS');
        process.exit(0);
    } else {
        console.log('\nGraph rendering: FAIL');
        process.exit(1);
    }
})();
