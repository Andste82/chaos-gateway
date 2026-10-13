import { defineConfig } from '@playwright/test';

// End-to-end tests run against the built app (`vite preview`) and a mocked API (e2e/mock.ts). The
// flows against the real stack in the testbed have their own config, playwright.testbed.config.ts,
// started by TestFirstStartInTheBrowserUntilADiscoveredDevice. Chromium runs without its sandbox:
// the devcontainer is unprivileged and runs as root.
export default defineConfig({
  testDir: 'e2e',
  webServer: {
    command: 'npm run build && npm run preview -- --host 127.0.0.1 --port 4173 --strictPort',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  use: {
    baseURL: 'http://127.0.0.1:4173',
    launchOptions: { args: ['--no-sandbox'] },
  },
});
