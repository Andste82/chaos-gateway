import { defineConfig } from '@playwright/test';

// End-to-end tests run against the built app (`vite preview`) and a mocked API (e2e/mock.ts). The
// flows against the real stack in the testbed have their own config, playwright.testbed.config.ts,
// started by TestFirstStartInTheBrowserUntilADiscoveredDevice. Chromium runs without its sandbox:
// the devcontainer is unprivileged and runs as root.
//
// Under load (a busy CI runner, --repeat-each) the first render after page.goto can take longer
// than Playwright's default 5 s, so assertions wait up to 10 s. In CI a failed test runs once
// more; the report lists such a test as "flaky", so it stays visible instead of failing the job
// on one slow cold start.
//
// E2E_PORT sets the port of the preview server (default 4173), so runs in several worktrees can
// overlap, each against its own build. A server already running on the port is reused only with
// E2E_REUSE=1 (never in CI); otherwise the run fails on a busy port instead of silently testing
// another worktree's build.
const port = Number(process.env.E2E_PORT || 4173);
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error(`E2E_PORT must be a TCP port, got ${JSON.stringify(process.env.E2E_PORT)}`);
}
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: 'e2e',
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  webServer: {
    command: `npm run build && npm run preview -- --host 127.0.0.1 --port ${port} --strictPort`,
    url: baseURL,
    reuseExistingServer: !process.env.CI && process.env.E2E_REUSE === '1',
    timeout: 120_000,
  },
  use: {
    baseURL,
    launchOptions: { args: ['--no-sandbox'] },
  },
});
