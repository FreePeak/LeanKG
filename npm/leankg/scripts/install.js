// LeanKG npm postinstall: download the prebuilt binary for the
// current platform + arch from the official GitHub release.
//
// Behavior:
//   - Resolve latest release tag from GitHub API.
//   - Pick asset matching `<platform>-<arch>` (no Rust toolchain
//     required).
//   - Save the binary next to bin/leankg.js as
//     `bin/leankg-<platform>-<arch>[.exe]` and chmod 0755.
//   - If the platform is unsupported, print a helpful error
//     pointing to the manual install path.

const { execSync } = require('node:child_process');
const { createWriteStream, existsSync, mkdirSync, chmodSync } =
  require('node:fs');
const { dirname, join } = require('node:path');
const https = require('node:https');

const REPO = 'FreePeak/LeanKG';
const PLATFORM = process.platform;
const ARCH = process.arch;
const EXE_SUFFIX = PLATFORM === 'win32' ? '.exe' : '';

function assetName(platform, arch) {
  // Maps Node's platform/arch tuples to LeanKG release asset names.
  const p = platform === 'win32' ? 'windows' : platform;
  const a = arch === 'x64' ? 'amd64' : arch;
  return `leankg-${p}-${a}${EXE_SUFFIX}`;
}

function latestTag() {
  return new Promise((resolve, reject) => {
    const req = https.get(
      `https://api.github.com/repos/${REPO}/releases/latest`,
      { headers: { 'User-Agent': 'leankg-npm' } },
      (res) => {
        if (res.statusCode !== 200) {
          return reject(
            new Error(`failed to fetch latest release: ${res.statusCode}`)
          );
        }
        let body = '';
        res.on('data', (c) => (body += c));
        res.on('end', () => {
          try {
            const json = JSON.parse(body);
            resolve(json.tag_name);
          } catch (e) {
            reject(e);
          }
        });
      }
    );
    req.on('error', reject);
  });
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      url,
      { headers: { 'User-Agent': 'leankg-npm' } },
      (res) => {
        if (res.statusCode === 302 || res.statusCode === 301) {
          // Follow redirect
          return download(res.headers.location, dest).then(resolve, reject);
        }
        if (res.statusCode !== 200) {
          return reject(
            new Error(`download failed: ${res.statusCode} for ${url}`)
          );
        }
        const f = createWriteStream(dest);
        res.pipe(f);
        f.on('finish', () => f.close(resolve));
        f.on('error', reject);
      }
    );
    req.on('error', reject);
  });
}

async function main() {
  const wanted = assetName(PLATFORM, ARCH);
  const binDir = join(__dirname, '..', 'bin');
  mkdirSync(binDir, { recursive: true });
  const dest = join(binDir, `leankg-${PLATFORM}-${ARCH}${EXE_SUFFIX}`);

  if (existsSync(dest)) {
    return; // already installed
  }

  let tag;
  try {
    tag = await latestTag();
  } catch (e) {
    // Network failure or rate-limit — fall back to a pinned version.
    tag = 'v0.17.9';
  }

  const url = `https://github.com/${REPO}/releases/download/${tag}/${wanted}`;
  console.log(`leankg: downloading ${url}`);
  try {
    await download(url, dest);
  } catch (e) {
    console.error(
      `leankg: failed to fetch ${url}: ${e.message}\n` +
        'If you are behind a corporate proxy, set HTTPS_PROXY.\n' +
        'Alternatively, build from source: cargo install --git https://github.com/FreePeak/LeanKG leankg'
    );
    process.exit(1);
  }
  try {
    chmodSync(dest, 0o755);
  } catch (_) {
    // ignore on Windows
  }
  console.log(`leankg: installed ${dest}`);
}

main().catch((e) => {
  console.error(`leankg postinstall failed: ${e.message}`);
  process.exit(1);
});
