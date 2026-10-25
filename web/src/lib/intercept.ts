// The exchanges of the interception proxy in the UI (plan §2.8, §2.17 "Interception UI", M23c): the filters of the table and the
// parameters of `GET /intercept/flows` they become, an exchange in words (what it asked for, how it ended, how big it was), the rules
// that acted on it, and the key of a row. The log is the gateway's: the newest 20000 exchanges in memory.
import type { InterceptFlow, ListInterceptFlowsParams } from '../api/generated/model';
import { createOverlayBodyFaultOneDestinationHostnameOneRegExp as hostRegExp } from '../api/generated/zod/overlays/overlays';
import { formatBytes } from './format';

/** The filters of the table: a device, a host name (exact or `*.suffix`) and whether a rule acted. */
export interface FlowFilters {
  /** The device's id; empty: every device. */
  device: string;
  host: string;
  faulted: boolean;
}

export const noFilters = (): FlowFilters => ({ device: '', host: '', faulted: false });

/** How many exchanges a page asks for and the most the table shows (the API's limit per request). */
export const pageSize = 100;
export const maxShown = 500;

/** The problem of the host filter, if it is no host name; empty is fine (every host). */
export function hostFilterError(host: string): string | undefined {
  const h = host.trim();
  return !h || hostRegExp.test(h) ? undefined : `${h} is no host name: a name like api.example.com, or *.example.com.`;
}

/** The parameters of the request the filters make; a host that is no name is not sent. */
export function flowParams(f: FlowFilters, limit: number): ListInterceptFlowsParams {
  const p: ListInterceptFlowsParams = { limit: Math.min(limit, maxShown) };
  if (f.device) p.device = f.device;
  if (f.host.trim() && !hostFilterError(f.host)) p.host = f.host.trim();
  if (f.faulted) p.faulted = true;
  return p;
}

/** Whether a filter is set (the empty table then says that nothing matches, not that nothing was seen). */
export function filtered(f: FlowFilters): boolean {
  return !!f.device || !!f.host.trim() || f.faulted;
}

export type FlowKind = InterceptFlow['protocol'];

export const kindLabels: Record<FlowKind, { label: string; hint: string }> = {
  http: { label: 'HTTP', hint: 'An HTTP/1.1 or HTTP/2 request and its response' },
  websocket: { label: 'WebSocket', hint: 'A WebSocket connection: its handshake, then its messages counted' },
  tcp: { label: 'Stream', hint: 'A connection that is no HTTP (MQTT over TLS): its bytes are counted, not read' },
};

/** What an exchange asked for: "GET api.example.com/v1/a"; a stream: its host, or the address the device meant. */
export function requestText(f: InterceptFlow): string {
  const host = f.host || f.original_dst;
  if (!f.method && f.path === undefined) return host;
  return `${f.method ? f.method + ' ' : ''}${host}${f.path ?? ''}`;
}

/** The rules that acted on an exchange, in the order they acted, as chips: a word and whether it stopped the exchange. */
export const actionLabels: Record<NonNullable<InterceptFlow['actions']>[number], { label: string; blocking: boolean }> = {
  block: { label: 'Blocked', blocking: true },
  status: { label: 'Answered', blocking: true },
  delay: { label: 'Delayed', blocking: false },
  throttle: { label: 'Throttled', blocking: false },
  abort: { label: 'Aborted', blocking: true },
  modify: { label: 'Modified', blocking: false },
};

/** How the exchange ended for the device: the status code, or why there is none. */
export function outcomeText(f: InterceptFlow): string {
  if (f.status !== undefined) return String(f.status);
  if (f.protocol === 'tcp') return 'no HTTP';
  return f.error ? 'no answer' : '—';
}

/** The bytes of an exchange: "120 B ↑ · 1.2 KB ↓", and a WebSocket's messages. */
export function sizeText(f: InterceptFlow): string {
  const parts = [`${formatBytes(f.request_bytes ?? 0)} ↑`, `${formatBytes(f.response_bytes ?? 0)} ↓`];
  if (f.protocol === 'websocket') parts.push(`${f.messages ?? 0} messages`);
  return parts.join(' · ');
}

/** How long it took: "3.5 ms", "2.0 s". */
export function durationText(f: InterceptFlow): string {
  const ms = f.duration_ms;
  if (ms === undefined) return '—';
  return ms < 1000 ? `${ms < 10 ? ms.toFixed(1) : Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`;
}

/** The key of a row: an exchange has no id, so its time, client, destination and request, and a count for the ones that are the same. */
export function flowKeys(items: InterceptFlow[]): string[] {
  const seen = new Map<string, number>();
  return items.map((f) => {
    const base = `${f.time}|${f.client}|${f.original_dst}|${f.method ?? ''}|${f.host ?? ''}|${f.path ?? ''}`;
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    return n ? `${base}#${n}` : base;
  });
}
