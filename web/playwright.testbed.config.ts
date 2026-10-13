import { defineConfig } from '@playwright/test';

// End-to-end flows against the real stack (plan M12: "an E2E flow in the testbed"). They are not
// run on their own: the Go test TestFirstStartInTheBrowserUntilADiscoveredDevice (internal/api,
// build tags testbed and browser) builds the testbed, starts the gateway with the built web app and
// runs this configuration with the gateway's address and setup token in the environment. Under
// software emulation an apply takes minutes, hence the long timeouts.
const url = process.env.CHAOSGW_URL;
if (!url) throw new Error('CHAOSGW_URL is not set: run these tests through the Go test (docs/development.md, "Web UI")');

export default defineConfig({
  testDir: 'e2e-testbed',
  outputDir: process.env.PLAYWRIGHT_OUTPUT_DIR ?? 'test-results',
  timeout: Number(process.env.CHAOSGW_TEST_TIMEOUT ?? 20 * 60_000),
  expect: { timeout: 60_000 },
  retries: 0,
  workers: 1,
  reporter: 'list',
  use: {
    baseURL: url,
    actionTimeout: 60_000,
    navigationTimeout: 120_000,
    launchOptions: { args: ['--no-sandbox'] },
    trace: 'off',
  },
});
