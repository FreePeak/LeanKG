const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const REPO = 'your-org/LeanKG';
const BINARY_NAME = 'leanKG';

function getPlatform() {
  const platform = process.platform;
  const arch = process.arch;
  
  if (platform === 'darwin') {
    return arch === 'arm64' ? 'macos-arm64' : 'macos-x64';
  }
  if (platform === 'linux') {
    return arch === 'arm64' ? 'linux-arm64' : 'linux-x64';
  }
  throw new Error(`Unsupported platform: ${platform}-${arch}`);
}

function getBinaryUrl(version, platform) {
  return `https://github.com/${REPO}/releases/download/v${version}/${BINARY_NAME}-${platform}.tar.gz`;
}

function getInstallDir() {
  return path.join(__dirname, 'bin');
}

function installBinary() {
  const version = require('./package.json').version;
  const platform = getPlatform();
  const installDir = getInstallDir();
  
  console.log(`Installing LeanKG v${version} for ${platform}...`);
  
  if (!fs.existsSync(installDir)) {
    fs.mkdirSync(installDir, { recursive: true });
  }
  
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'leankg-'));
  const tarPath = path.join(tmpDir, 'binary.tar.gz');
  
  try {
    const url = getBinaryUrl(version, platform);
    console.log(`Downloading from ${url}...`);
    
    execSync(`curl -L -o "${tarPath}" "${url}"`, { stdio: 'inherit' });
    
    console.log('Extracting binary...');
    execSync(`tar -xzf "${tarPath}" -C "${installDir}"`, { stdio: 'inherit' });
    
    const binaryPath = path.join(installDir, BINARY_NAME);
    fs.chmodSync(binaryPath, 0o755);
    
    console.log(`Installed to ${binaryPath}`);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

try {
  installBinary();
} catch (error) {
  console.error('Installation failed:', error.message);
  console.error('You may need to install Rust from https://rustup.rs and run: cargo install leankg');
  process.exit(1);
}
