import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { API_BASE } from '../api/fetcher';

/** One request of the `</> API` panel: what a test would send for the same action. */
export interface ApiCall {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE';
  /** The path under /api/v1, with its query. */
  path: string;
  body?: unknown;
  /** Headers beyond the token's (Content-Type, If-Match). */
  headers?: Record<string, string>;
  /** What the request does, in a few words. */
  note?: string;
}

/**
 * The `</> API` panel (plan §2.17: every UI action has an API equivalent, and the UI can show it).
 * A screen says what it reads (`context`), an open dialog what it would send, and every action
 * records the requests it made (`last`); the panel shows the open dialog's requests, else the last
 * action, else the screen's requests, as HTTP and as curl.
 */
export const useApiPanelStore = defineStore('apiPanel', () => {
  const open = ref(false);
  const context = ref<{ title: string; calls: ApiCall[] }>();
  const draft = ref<{ title: string; calls: ApiCall[] }>();
  const last = ref<{ title: string; calls: ApiCall[] }>();

  function record(title: string, calls: ApiCall[]) {
    last.value = { title, calls };
  }

  /** A new screen: its own requests, and no action of another screen. */
  function screen(title: string, calls: ApiCall[]) {
    context.value = { title, calls };
    last.value = undefined;
    draft.value = undefined;
  }

  return { open, context, draft, last, record, screen };
});

/** A word in single quotes for a POSIX shell: an apostrophe in it ends the quote, is escaped and reopens it. */
function shellQuote(s: string): string {
  return `'${s.replaceAll("'", `'\\''`)}'`;
}

/** The request as curl, with an API token from the environment. */
export function curlOf(c: ApiCall, origin = typeof location !== 'undefined' ? location.origin : 'https://gateway'): string {
  const parts = [`curl -X ${c.method} ${shellQuote(origin + API_BASE + c.path)}`, `-H "Authorization: Bearer $CHAOSGW_TOKEN"`];
  for (const [k, v] of Object.entries(c.headers ?? {})) parts.push(`-H ${shellQuote(`${k}: ${v}`)}`);
  if (c.body !== undefined) {
    if (!c.headers?.['Content-Type']) parts.push(`-H 'Content-Type: application/json'`);
    parts.push(`-d ${shellQuote(JSON.stringify(c.body))}`);
  }
  return parts.join(' \\\n  ');
}

/**
 * The requests of a screen for the `</> API` panel; `calls` is read again when what it depends on
 * changes (the device of a device page).
 */
export function useApiScreen(title: string, calls: () => ApiCall[]): void {
  const store = useApiPanelStore();
  store.screen(title, calls());
  watch(calls, (c) => (store.context = { title, calls: c }), { deep: true });
}

/** The request as HTTP: "POST /api/v1/overlays" and the body. */
export function httpOf(c: ApiCall): string {
  const head = `${c.method} ${API_BASE}${c.path}`;
  const headers = Object.entries(c.headers ?? {}).map(([k, v]) => `${k}: ${v}`);
  return [head, ...headers, ...(c.body !== undefined ? ['', JSON.stringify(c.body, null, 2)] : [])].join('\n');
}
