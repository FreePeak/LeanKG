#!/usr/bin/env node

const { execSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const platform = process.platform;
const arch = process.arch;

const platformMap = {
  darwin: { os: 'macos', ext: '' },
  linux: { os: 'linux', ext: '' },
  win32: { os: 'windows', ext: '.exe' }
};

const archMap = {
  x64: 'x86_64',
  arm64: 'aarch64'
};

const pkg = require('../package.json');
const binDir = path.join(__dirname, '..', 'bin');
const binaryName = `leankg${platformMap[platform]?.ext || ''}`;
const binaryPath = path.join(binDir, binaryName);

console.log(`LeanKG MCP Server v${pkg.version}`);
console.log(`Platform: ${platform} ${arch}`);

if (fs.existsSync(binaryPath)) {
  console.log('Binary already installed.');
  makeExecutable(binaryPath);
  return;
}

const distBaseUrl = `https://github.com/anomalyco/LeanKG/releases/download/v${pkg.version}`;
const targetOs = platformMap[platform]?.os || 'linux';
const targetArch = archMap[arch] || 'x86_64';
const binaryUrl = `${distBaseUrl}/leankg-${targetOs}-${targetArch}${platform === 'win32' ? '.exe' : ''}`;

console.log(`Attempting to download pre-built binary from GitHub releases...`);
console.log(`URL: ${binaryUrl}`);

try {
  const https = require('https');
  const http = require('http');
  const protocol = binaryUrl.startsWith('https') ? https : http;
  
  const file = fs.createWriteStream(binaryPath);
  
  return new Promise((resolve, reject) => {
    protocol.get(binaryUrl, (response) => {
      if (response.statusCode === 302 || response.statusCode === 301) {
        const redirectUrl = response.headers.location;
        console.log(`Redirecting to: ${redirectUrl}`);
        protocol.get(redirectUrl, (redirectResponse) => {
          redirectResponse.pipe(file);
          file.on('finish', () => {
            file.close();
            makeExecutable(binaryPath);
            console.log('Binary downloaded successfully!');
            resolve();
          });
        }).on('error', (err) => {
          fs.unlink(binaryPath, () => {});
          reject(err);
        });
      } else if (response.statusCode === 200) {
        response.pipe(file);
        file.on('finish', () => {
          file.close();
          makeExecutable(binaryPath);
          console.log('Binary downloaded successfully!');
          resolve();
        });
      } else {
        file.close();
        fs.unlink(binaryPath, () => {});
        reject(new Error(`Download failed with status code: ${response.statusCode}`));
      }
    }).on('error', (err) => {
      fs.unlink(binaryPath, () => {});
      reject(err);
    });
  }).catch((err) => {
    console.log('');
    console.log('Pre-built binary not available.');
    console.log('To install from source, run:');
    console.log('  cargo install leankg');
    console.log('');
    console.log('Or build manually:');
    console.log('  git clone https://github.com/anomalyco/LeanKG.git');
    console.log('  cd LeanKG && cargo build --release');
    console.log('');
    console.log('The binary should be placed at: bin/leankg');
  });
} catch (err) {
  console.log('Installation note: Pre-built binaries will be available in future releases.');
  console.log('For now, please install via cargo: cargo install leankg');
}

function makeExecutable(binaryPath) {
  try {
    if (process.platform !== 'win32') {
      fs.chmodSync(binaryPath, 0o755);
    }
  } catch (err) {
    console.error('Warning: Could not make binary executable');
  }
}
