// Diagnostics in the UI (plan §2.12, §2.17 "Diagnostics"; M24): the form of a diagnostic with the checks the gateway makes
// (domain.ValidateDiagnosticRequest), the request it becomes, the field a refusal names, and the result in words: one
// sentence per type, and the steps of a path ("DNS ✓  Route ✓  TCP ✓  TLS ✗ (certificate has expired)").
import type { DiagnosticRequest, DiagnosticResult } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { durationMs } from './format';

export type DiagnosticType = 'path' | 'ping' | 'tcp' | 'udp' | 'dns' | 'http' | 'tls';

export interface DiagnosticTypeInfo {
  value: DiagnosticType;
  label: string;
  /** What it does, in a sentence. */
  hint: string;
}

export const diagnosticTypes: DiagnosticTypeInfo[] = [
  { value: 'path', label: 'Path', hint: 'DNS, route, TCP and TLS one after the other, up to the first step that fails.' },
  { value: 'ping', label: 'Ping', hint: 'ICMP echoes, 200 ms apart, with loss and round trip times.' },
  { value: 'tcp', label: 'TCP', hint: 'Connects to a port and closes the connection again.' },
  { value: 'udp', label: 'UDP', hint: 'Sends one datagram and waits for a reply; a closed port answers with ICMP port unreachable.' },
  { value: 'dns', label: 'DNS', hint: 'Asks for the A record of a name, the gateway’s DNS proxy or a server of your choice.' },
  { value: 'http', label: 'HTTP(S)', hint: 'One GET request; a redirect is shown, not followed.' },
  { value: 'tls', label: 'TLS', hint: 'The handshake and what the server presented: version, cipher, ALPN, the certificate chain and whether it verifies.' },
];

export function isDiagnosticType(s: string | undefined): s is DiagnosticType {
  return diagnosticTypes.some((t) => t.value === s);
}

export interface DiagnosticForm {
  type: DiagnosticType;
  /** The host to reach; the name to resolve for `dns`; unused for `http`. */
  host: string;
  /** Required for path, TCP and UDP; for DNS the server's port and for TLS the port, with their defaults when empty. */
  port: string;
  count: string;
  timeout: string;
  /** `dns`: the server to ask; empty is the gateway's DNS proxy. */
  server: string;
  url: string;
  sni: string;
  payload: string;
  /** `path`: the TLS step. */
  tls: boolean;
}

export type DiagnosticField = 'host' | 'port' | 'count' | 'timeout' | 'server' | 'url' | 'sni' | 'payload';

export function emptyDiagnosticForm(type: DiagnosticType = 'path'): DiagnosticForm {
  return { type, host: '', port: '', count: '', timeout: '', server: '', url: '', sni: '', payload: '', tls: true };
}

/** What the form of a type has: the fields it shows, and for the port whether it is required. */
export function fieldsOf(type: DiagnosticType): { host: boolean; port: false | 'required' | 'optional'; count: boolean; server: boolean; url: boolean; sni: boolean; payload: boolean; tls: boolean } {
  return {
    host: type !== 'http',
    port: type === 'path' || type === 'tcp' || type === 'udp' ? 'required' : type === 'dns' || type === 'tls' ? 'optional' : false,
    count: type === 'ping',
    server: type === 'dns',
    url: type === 'http',
    sni: type === 'tls' || type === 'path',
    payload: type === 'udp',
    tls: type === 'path',
  };
}

// ---- the checks of the gateway (domain.ValidDiagnosticHost and ValidateDiagnosticRequest)

const hostPattern = /^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$/;
const ipv4Pattern = /^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$/;

/** An IPv4 address or a host name, without a port, a path or a wildcard. */
export function isValidHost(s: string): boolean {
  if (ipv4Pattern.test(s)) return true;
  if (s.length === 0 || s.length > 253 || !hostPattern.test(s)) return false;
  return /[^0-9]/.test(s.slice(s.lastIndexOf('.') + 1)); // a last label of digits only is a malformed address
}

const MAX_MS = 60_000;
const PING_INTERVAL_MS = 200;

/** The URL of an http diagnostic, or undefined when the gateway would refuse it. */
export function parseHttpUrl(text: string): URL | undefined {
  // the browser reads `http:///path` as the host `path`; the gateway sees no host in it
  if (!/^https?:\/\/[^/?#]/i.test(text)) return undefined;
  let u: URL;
  try {
    u = new URL(text);
  } catch {
    return undefined;
  }
  if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.username || u.password) return undefined;
  if (!isValidHost(u.hostname)) return undefined;
  return u;
}

function portError(text: string, required: boolean): string | undefined {
  const t = text.trim();
  if (t === '') return required ? 'The port is needed.' : undefined;
  const n = Number(t);
  return /^[0-9]+$/.test(t) && n >= 1 && n <= 65535 ? undefined : 'A port is a number from 1 to 65535.';
}

/** The problems of the form, by field; empty when the gateway can run it. */
export function diagnosticErrors(f: DiagnosticForm): Partial<Record<DiagnosticField, string>> {
  const e: Partial<Record<DiagnosticField, string>> = {};
  const fields = fieldsOf(f.type);
  if (fields.host) {
    const host = f.host.trim();
    if (host === '') e.host = f.type === 'dns' ? 'The name to resolve is needed.' : 'The host is needed.';
    else if (!isValidHost(host)) e.host = f.type === 'dns' ? 'A host name, without wildcard.' : 'An IPv4 address or a host name, without a port or a path.';
  }
  if (fields.port) {
    const msg = portError(f.port, fields.port === 'required');
    if (msg) e.port = msg;
  }
  if (fields.server && f.server.trim() !== '' && !isValidHost(f.server.trim())) e.server = 'An IPv4 address or a host name, without a port.';
  if (fields.url) {
    if (f.url.trim() === '') e.url = 'The URL is needed.';
    else if (!parseHttpUrl(f.url.trim())) e.url = 'An http:// or https:// URL with a host name or an IPv4 address, without credentials.';
  }
  if (fields.sni && f.sni.trim() !== '' && !isValidHost(f.sni.trim())) e.sni = 'A host name.';
  if (fields.payload && f.payload.length > 1024) e.payload = 'At most 1024 characters.';
  let timeout = 5000;
  if (f.timeout.trim() !== '') {
    const ms = durationMs(f.timeout.trim());
    if (ms === undefined || !/^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$/.test(f.timeout.trim())) e.timeout = 'A duration like 5s or 500ms.';
    else if (ms < 10) e.timeout = 'At least 10ms.';
    else if (ms > MAX_MS) e.timeout = 'At most 60s.';
    else timeout = ms;
  }
  if (fields.count && f.count.trim() !== '') {
    const n = Number(f.count.trim());
    if (!/^[0-9]+$/.test(f.count.trim()) || n < 1 || n > 100) e.count = 'A number from 1 to 100.';
    else if (!e.timeout && (n - 1) * PING_INTERVAL_MS + timeout > MAX_MS) e.count = 'The echoes and the timeout take longer than 60s.';
  }
  return e;
}

/** The request the form becomes. */
export function formToDiagnosticRequest(f: DiagnosticForm): DiagnosticRequest {
  const req: DiagnosticRequest = { type: f.type, host: f.host.trim() };
  const fields = fieldsOf(f.type);
  if (f.type === 'http') {
    const url = f.url.trim();
    req.host = parseHttpUrl(url)?.hostname ?? url;
    req.url = url;
  }
  if (f.type === 'dns' && f.server.trim() !== '') {
    req.dns_name = f.host.trim();
    req.host = f.server.trim();
  }
  // the port of a DNS diagnostic is the server's: without a server the gateway's proxy answers
  if (fields.port && f.port.trim() !== '' && !(f.type === 'dns' && f.server.trim() === '')) req.port = Number(f.port.trim());
  if (fields.count && f.count.trim() !== '') req.count = Number(f.count.trim());
  if (f.timeout.trim() !== '') req.timeout = f.timeout.trim();
  if (fields.sni && f.sni.trim() !== '') req.sni = f.sni.trim();
  if (fields.payload && f.payload !== '') req.payload = f.payload;
  if (fields.tls && !f.tls) req.tls = false;
  return req;
}

/** The form a request would have come from (the link of a device's page, the query of the address). */
export function formFromQuery(q: Record<string, unknown>): DiagnosticForm {
  const one = (k: string) => (typeof q[k] === 'string' ? (q[k] as string) : '');
  const type = isDiagnosticType(one('type')) ? (one('type') as DiagnosticType) : 'path';
  const f = emptyDiagnosticForm(type);
  f.host = one('host');
  f.port = one('port');
  return f;
}

/** The field a refusal of the gateway names (`/port`, `/dns_name`), or undefined when it names none. */
export function diagnosticFieldOf(path: string | undefined): DiagnosticField | undefined {
  switch (path) {
    case '/host':
    case '/dns_name':
      return 'host';
    case '/port':
    case '/count':
    case '/timeout':
    case '/url':
    case '/sni':
    case '/payload':
      return path.slice(1) as DiagnosticField;
    default:
      return undefined;
  }
}

// ---- the request and the result in words

/** Where the request goes: "broker.example.com:8883", "https://api.example.com/health". */
export function describeTarget(req: DiagnosticRequest): string {
  if (req.type === 'http' && req.url) return req.url;
  if (req.type === 'dns') return req.dns_name ? `${req.dns_name} @ ${req.host}${req.port ? `:${req.port}` : ''}` : `${req.host} @ the DNS proxy`;
  return req.port ? `${req.host}:${req.port}` : req.host;
}

export function formatMs(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || !Number.isFinite(ms)) return '—';
  if (ms >= 100) return `${Math.round(ms)} ms`;
  if (ms >= 10) return `${ms.toFixed(1)} ms`;
  return `${ms.toFixed(2)} ms`;
}

export function resultTone(r: Pick<DiagnosticResult, 'ok'>): { tone: DotTone; label: string } {
  return r.ok ? { tone: 'normal', label: 'Succeeded' } : { tone: 'block', label: 'Failed' };
}

export interface PathStep {
  step: 'dns' | 'route' | 'tcp' | 'tls';
  label: string;
  state: 'ok' | 'failed' | 'not run';
  detail: string;
}

const stepLabels: Record<PathStep['step'], string> = { dns: 'DNS', route: 'Route', tcp: 'TCP', tls: 'TLS' };

/**
 * The steps of a path result: the four of them, those after the failed step as "not run". A path that succeeded with
 * three steps was asked without the TLS step, which then is not listed.
 */
export function pathSteps(r: Pick<DiagnosticResult, 'ok' | 'path'>): PathStep[] {
  const done = r.path ?? [];
  const order: PathStep['step'][] = r.ok && done.length === 3 ? ['dns', 'route', 'tcp'] : ['dns', 'route', 'tcp', 'tls'];
  return order.map((step) => {
    const d = done.find((s) => s.step === step);
    return { step, label: stepLabels[step], state: d ? (d.ok ? 'ok' : 'failed') : 'not run', detail: d?.detail ?? '' };
  });
}

/** "DNS ✓  Route ✓  TCP ✓  TLS ✗ (certificate has expired)" — the line of plan §2.12. */
export function pathLine(r: Pick<DiagnosticResult, 'ok' | 'path'>): string {
  return pathSteps(r)
    .map((s) => (s.state === 'ok' ? `${s.label} ✓` : s.state === 'failed' ? `${s.label} ✗${s.detail ? ` (${s.detail})` : ''}` : `${s.label} –`))
    .join('  ');
}

/** What happened, in a sentence; the error when it failed. */
export function resultSentence(r: DiagnosticResult): string {
  if (!r.ok && r.error) return r.error;
  switch (r.type) {
    case 'ping': {
      const p = r.ping;
      if (!p) return 'Done.';
      return `${p.received} of ${p.sent} replies, ${p.loss_percent} % loss, average ${formatMs(p.rtt_avg_ms)}`;
    }
    case 'tcp':
      return `Connected in ${formatMs(r.connect?.rtt_ms)}`;
    case 'udp':
      return `${r.connect?.detail ?? 'A reply came back'} in ${formatMs(r.connect?.rtt_ms)}`;
    case 'dns':
      return `${(r.dns?.answers ?? []).join(', ') || 'no answer'} · ${r.dns?.rcode ?? ''} in ${formatMs(r.dns?.rtt_ms)}`;
    case 'http':
      return `HTTP ${r.http?.status} in ${formatMs(r.http?.rtt_ms)}`;
    case 'tls':
      return `${r.tls?.version ?? 'TLS'}, ${r.tls?.cipher ?? ''}; the certificate verifies`;
    case 'path':
      return pathLine(r);
    default:
      return 'Done.';
  }
}
