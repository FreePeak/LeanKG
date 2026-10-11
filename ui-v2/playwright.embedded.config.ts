import { defineConfig, devices } from '@playwright/test';

// Run the e2e suite against the EMBEDDED ui-v2 build — the one `go build`
// ships and `leankg serve --ui` serves — instead of a vite dev server.
//
// The default playwright.config.ts starts `npm run dev` and assumes a backend
// on :8080, which is why this suite has never run in CI: no workflow has a
// frontend job, and a bare `npx playwright test` on a developer machine skipped
// everything (the specs self-skip when no server answers) or failed on a
// missing browser. Verified 2026-10-11: 11/11 specs pass against the embedded
// build served by `leankg serve --ui`.
//
//   go build -o /tmp/leankg ./cmd/leankg
//   /tmp/leankg serve --http 127.0.0.1:9801 --ui 127.0.0.1:9802 --project . &
//   FRONTEND_URL=http://127.0.0.1:9802 BACKEND_URL=http://127.0.0.1:9802 E2E=1 \
//     npx playwright test --config playwright.embedded.config.ts
//
// E2E=1 forces the specs' own availability probe to pass, so a missing server
// fails loudly instead of skipping.
const FRONTEND_URL = process.env.FRONTEND_URL ?? 'http://127.0.0.1:9802';
const BACKEND_URL = process.env.BACKEND_URL ?? FRONTEND_URL;

const systemChrome =
  process.env.PLAYWRIGHT_CHROME_PATH ??
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

export default defineConfig({
  testDir: './e2e',
  timeout: 90_000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  use: {
    baseURL: FRONTEND_URL,
    trace: 'off',
    screenshot: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // Prefer the machine's Chrome over a downloaded headless shell so the
        // suite runs without `npx playwright install` (a ~200MB download the
        // CI runner does not need for one run).
        channel: 'chrome',
        launchOptions: { executablePath: systemChrome },
      },
    },
  ],
  metadata: { BACKEND_URL, FRONTEND_URL },
});
