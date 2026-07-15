#!/usr/bin/env node
// LeanKG npm wrapper. Forwards all args to the downloaded prebuilt
// binary at node_modules/leankg/bin/leankg. On first install the
// `postinstall` script downloads the right binary for the current
// platform + arch. If the binary is missing, the wrapper attempts
// to install on demand (e.g. after switching Node versions).

const { spawnSync } = require('node:child_process');
const { existsSync, chmodSync } = require('node:fs');
const { join } = require('node:path');

const binDir = __dirname;
const platform = process.platform;
const arch = process.arch;
const exeSuffix = platform === 'win32' ? '.exe' : '';
const binPath = join(binDir, `leankg-${platform}-${arch}${exeSuffix}`);

function ensureBinary() {
  if (existsSync(binPath)) {
    return true;
  }
  console.error(
    `leankg binary not found at ${binPath}. Running postinstall...`
  );
  const result = spawnSync(
    process.execPath,
    [join(binDir, '..', 'scripts', 'install.js')],
    { stdio: 'inherit' }
  );
  if (result.status !== 0 || !existsSync(binPath)) {
    console.error(
      `leankg: failed to install binary for ${platform}/${arch}.\n` +
        'See https://github.com/FreePeak/LeanKG/releases for manual install.'
    );
    process.exit(1);
  }
  return true;
}

ensureBinary();

try {
  chmodSync(binPath, 0o755);
} catch (_) {
  // best-effort: chmod is not critical on Windows.
}

const result = spawnSync(binPath, process.argv.slice(2), {
  stdio: 'inherit',
});

if (result.error) {
  console.error(`leankg: ${result.error.message}`);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
