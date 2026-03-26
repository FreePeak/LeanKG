#!/usr/bin/env node

const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');

const binPath = path.join(__dirname, 'bin', 'leankg');

if (!fs.existsSync(binPath)) {
  console.error('Error: LeanKG binary not found.');
  console.error('Please run: npm install -g @leankg/mcp-server');
  console.error('Or build from source: cargo install leankg');
  process.exit(1);
}

const args = process.argv.slice(2);
const child = spawn(binPath, args, {
  stdio: 'inherit',
  shell: process.platform === 'win32'
});

child.on('exit', (code) => {
  process.exit(code);
});
