import { defineConfig } from '@playwright/test';

// The screenshots of the README (docs/images/): the built app against the mocked API of the e2e tests (e2e/mock.ts), one page after the other.
// `npm run screenshots` writes them; run it again when a screen changes. Chromium runs without its sandbox, as in playwright.config.ts.
const rawPort = process.env.E2E_PORT || '4173';
const port = Number(rawPort);
if (!/^\d+$/.test(rawPort) || port < 1 || port > 65535) {
  throw new Error(`E2E_PORT must be a TCP port, got ${JSON.stringify(process.env.E2E_PORT)}`);
}
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: 'screenshots',
  workers: 1,
  expect: { timeout: 10_000 },
  webServer: {
    command: `npm run build && npm run preview -- --host 127.0.0.1 --port ${port} --strictPort`,
    url: baseURL,
    reuseExistingServer: process.env.E2E_REUSE === '1',
    timeout: 300_000,
  },
  use: {
    baseURL,
    viewport: { width: 1440, height: 900 },
    launchOptions: { args: ['--no-sandbox'] },
  },
});
