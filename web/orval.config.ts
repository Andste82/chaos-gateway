import { defineConfig } from 'orval';

// Generated clients (plan §2.15, §3.7). `../api/openapi.yaml` is the source of truth.
//   api      typed Vue Query hooks for the UI
//   apiZod   Zod schemas for client-side validation
//   fetch    plain fetch client without Vue, for Jest/Vitest suites (clients/typescript)
const input = '../api/openapi.yaml';

export default defineConfig({
  api: {
    input,
    output: {
      mode: 'tags-split',
      target: 'src/api/generated/hooks',
      schemas: 'src/api/generated/model',
      client: 'vue-query',
      override: { mutator: { path: 'src/api/fetcher.ts', name: 'customFetch' } },
    },
  },
  apiZod: {
    input,
    output: {
      mode: 'tags-split',
      target: 'src/api/generated/zod',
      client: 'zod',
    },
  },
  fetch: {
    input,
    output: {
      mode: 'tags-split',
      target: '../clients/typescript/src',
      schemas: '../clients/typescript/src/model',
      client: 'fetch',
    },
  },
});
