#!/usr/bin/env node
const { spawn } = require('child_process');

const BIN = './target/release/leankg';

const proc = spawn(BIN, ['mcp-stdio']);

let id = 1;
const pending = new Map();

function send(method, params, notification = false) {
    return new Promise((resolve, reject) => {
        const reqId = id++;
        const msg = notification
            ? JSON.stringify({ jsonrpc: '2.0', method, params })
            : JSON.stringify({ jsonrpc: '2.0', id: reqId, method, params });
        pending.set(notification ? -1 : reqId, { resolve, reject });
        proc.stdin.write(msg + '\n');

        setTimeout(() => {
            if (pending.has(notification ? -1 : reqId)) {
                pending.delete(notification ? -1 : reqId);
                if (!notification) reject(new Error('Request timeout'));
            }
        }, 30000);
    });
}

async function callTool(name, args = {}) {
    const result = await send('tools/call', { name, arguments: args });
    return result;
}

async function main() {
    proc.stdout.on('data', (data) => {
        const lines = data.toString().split('\n').filter(l => l.trim());
        for (const line of lines) {
            try {
                const resp = JSON.parse(line);
                if (resp.id !== undefined && resp.id !== null && pending.has(resp.id)) {
                    const { resolve } = pending.get(resp.id);
                    pending.delete(resp.id);
                    resolve(resp.result);
                }
            } catch (e) {
                // skip non-JSON
            }
        }
    });

    proc.stderr.on('data', (data) => {
        const msg = data.toString().trim();
        if (msg && !msg.includes('MCP stdio') && !msg.includes('expect initialized')) {
            console.error('STDERR:', msg);
        }
    });

    // Initialize
    await send('initialize', {
        protocolVersion: '2024-11-05',
        capabilities: {},
        clientInfo: { name: 'test', version: '0.1' }
    });

    // Send initialized as a true notification (no id)
    proc.stdin.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized', params: {}}) + '\n');
    await new Promise(r => setTimeout(r, 500));
    console.log('Initialized\n');

    const tests = [
        { name: 'mcp_hello', args: {} },
        { name: 'search_code', args: { query: 'main' } },
        { name: 'get_clusters', args: { limit: 5 } },
        { name: 'get_context', args: { file: 'src/main.rs' } },
        { name: 'get_code_tree', args: { max_files: 5 } },
        { name: 'get_traceability', args: { element: 'src/main.rs::main' } },
        { name: 'get_dependencies', args: { file: 'src/main.rs' } },
        { name: 'get_doc_tree', args: {} },
        { name: 'orchestrate', args: { intent: 'context for main' } },
        { name: 'get_dependents', args: { file: 'src/main.rs' } },
        { name: 'get_callers', args: { function: 'main' } },
        { name: 'get_call_graph', args: { function: 'src/main.rs::main' } },
    ];

    for (const test of tests) {
        try {
            console.log(`=== ${test.name} ===`);
            const result = await callTool(test.name, test.args);
            const str = JSON.stringify(result);
            console.log(str.length > 600 ? str.slice(0, 600) + '...' : str);
            console.log('');
        } catch (e) {
            console.log(`ERROR: ${e.message}\n`);
        }
    }

    proc.kill();
    process.exit(0);
}

main().catch(e => {
    console.error('Fatal:', e);
    proc.kill();
    process.exit(1);
});
