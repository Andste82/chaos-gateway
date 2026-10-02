import tailwindcss from '@tailwindcss/vite';
import vue from '@vitejs/plugin-vue';
import { defineConfig } from 'vitest/config';

// `make dev` starts the API on 127.0.0.1:8443; override with CHAOSGW_API.
const api = process.env.CHAOSGW_API ?? 'https://127.0.0.1:8443';

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    proxy: { '/api': { target: api, secure: false } },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
});
