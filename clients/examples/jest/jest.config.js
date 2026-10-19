/** @type {import('jest').Config} */
module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  // points the generated client at the gateway and gives it the token (setup.ts)
  setupFiles: ['<rootDir>/setup.ts'],
  testMatch: ['<rootDir>/*.test.ts'],
  // a run of a scenario lasts seconds
  testTimeout: 60000,
};
