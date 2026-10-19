/** @type {import('jest').Config} */
module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  // points the generated client at the gateway and gives it the token (setup.ts)
  setupFiles: ['<rootDir>/setup.ts'],
  testMatch: ['<rootDir>/*.test.ts'],
  // a run of a scenario lasts seconds
  testTimeout: 60000,
  // one test file at a time: they share one device, and the precondition of a scenario run (no foreign overlay on the
  // target) fails while a fault of another file is on it
  maxWorkers: 1,
};
