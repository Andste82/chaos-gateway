// The form of a DNS fault (plan §2.6, §2.17 "Add fault", M23b): the action the DNS proxy takes (NXDOMAIN, SERVFAIL, no
// answer, a delayed answer, a wrong answer, a truncated answer, a short TTL), the names it applies to (exact or `*.suffix`;
// none: all names) and what the action needs (the delay, the addresses, the TTL), the checks under the fields and the
// `dns` part of the request it becomes. The "Add fault" dialog (an overlay of the kind `dns`) and the step dialog (the
// step type `dns`) use the same fields, the same checks and the same body.
import type { DnsFault } from '../api/generated/model';
import {
  createOverlayBodyDnsDelayOneRegExp as durationRegExp,
  createOverlayBodyDnsNamesItemRegExp as nameRegExp,
} from '../api/generated/zod/overlays/overlays';
import { durationMs } from './format';
import { parseIPv4 } from './networks';

export type DnsAction = DnsFault['action'];

export interface DnsForm {
  action: DnsAction;
  /** Host names, separated by commas, spaces or lines; empty is every name. */
  names: string;
  /** The delay of a delayed answer: a duration like `500ms` or `2s`. */
  delay: string;
  /** The addresses of a wrong answer, separated by commas, spaces or lines. */
  answers: string;
  /** The TTL of a short TTL: a duration like `5s`; `0s` is an answer that must not be cached. */
  ttl: string;
}

export type DnsField = 'dnsAction' | 'dnsNames' | 'dnsDelay' | 'dnsAnswers' | 'dnsTtl';

export const dnsActions: { value: DnsAction; label: string; hint: string }[] = [
  { value: 'nxdomain', label: 'NXDOMAIN · the name does not exist', hint: 'The proxy answers "no such name" without asking the upstream resolver.' },
  { value: 'servfail', label: 'SERVFAIL · the resolver fails', hint: 'The proxy answers "server failure" without asking the upstream resolver.' },
  { value: 'timeout', label: 'Timeout · no answer', hint: 'The query is not answered, over UDP and over TCP: the client waits for its own timeout and retries.' },
  { value: 'delay', label: 'Delayed answer', hint: 'The real answer is sent after the delay. A client whose timeout is shorter than the delay never sees it.' },
  { value: 'wrong_answer', label: 'Wrong answer · other addresses', hint: 'The addresses of an A query are replaced, for example to point the broker at a mock server. Other query types are answered as usual.' },
  { value: 'truncate', label: 'Truncated · forces TCP', hint: 'Over UDP the proxy answers empty with the truncation bit set, so the client asks again over TCP, which gets the real answer.' },
  { value: 'short_ttl', label: 'Short TTL', hint: 'Every TTL of the answer is rewritten to this one. 0s makes an answer that must not be cached.' },
];

export function emptyDns(): DnsForm {
  return { action: 'nxdomain', names: '', delay: '2s', answers: '', ttl: '5s' };
}

/** Words or lines as typed: "a, b c" → ["a", "b", "c"]. */
function list(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/** The problems of the form, by field; empty when the fault can be sent. */
export function dnsErrors(f: DnsForm): Partial<Record<DnsField, string>> {
  const e: Partial<Record<DnsField, string>> = {};
  const names = list(f.names);
  const bad = names.find((n) => !nameRegExp.test(n));
  if (bad) e.dnsNames = `${bad} is no host name: a name like broker.example.com, or *.example.com.`;
  else if (names.some((n, i) => names.findIndex((m) => m.toLowerCase() === n.toLowerCase()) !== i)) e.dnsNames = 'Each name once.';
  switch (f.action) {
    case 'delay': {
      const d = f.delay.trim();
      if (!durationRegExp.test(d) || (durationMs(d) ?? 0) <= 0) e.dnsDelay = 'A delay longer than zero, such as 500ms or 2s.';
      break;
    }
    case 'wrong_answer': {
      const answers = list(f.answers);
      const bad = answers.find((a) => {
        const ip = parseIPv4(a);
        return ip === undefined || ip === 0;
      });
      if (answers.length === 0) e.dnsAnswers = 'Give the IPv4 address the name is answered with.';
      else if (bad) e.dnsAnswers = `${bad} is no IPv4 address.`;
      break;
    }
    case 'short_ttl':
      if (!durationRegExp.test(f.ttl.trim())) e.dnsTtl = 'A TTL such as 5s or 0s.';
      break;
  }
  return e;
}

/** The DNS fault of the form: only the fields its action uses (the gateway refuses the others). */
export function dnsBody(f: DnsForm): DnsFault {
  const out: DnsFault = { action: f.action };
  const names = list(f.names);
  if (names.length) out.names = names;
  if (f.action === 'delay') out.delay = f.delay.trim();
  if (f.action === 'wrong_answer') out.answers = list(f.answers);
  if (f.action === 'short_ttl') out.ttl = f.ttl.trim();
  return out;
}

/** The form of a DNS fault. */
export function dnsToForm(d: DnsFault | undefined): DnsForm {
  const f = emptyDns();
  if (!d) return f;
  f.action = d.action;
  f.names = (d.names ?? []).join(', ');
  if (d.delay !== undefined) f.delay = d.delay;
  if (d.answers) f.answers = d.answers.join(', ');
  if (d.ttl !== undefined) f.ttl = d.ttl;
  return f;
}

/** What the action does, in words: "NXDOMAIN", "answers delayed by 2s", "answers 10.9.0.1", "TTL set to 5s". */
export function dnsEffect(d: DnsFault): string {
  switch (d.action) {
    case 'nxdomain':
      return 'NXDOMAIN (no such name)';
    case 'servfail':
      return 'SERVFAIL';
    case 'timeout':
      return 'no answer';
    case 'delay':
      return `answers delayed by ${d.delay ?? '?'}`;
    case 'wrong_answer':
      return `answers ${(d.answers ?? []).join(', ') || '?'}`;
    case 'truncate':
      return 'truncated answers (the client falls back to TCP)';
    case 'short_ttl':
      return `TTL set to ${d.ttl ?? '?'}`;
    default:
      return String(d.action);
  }
}

/** "DNS queries for broker.example.com", "DNS queries for every name". */
export function dnsScope(d: DnsFault): string {
  const names = d.names ?? [];
  return names.length ? `DNS queries for ${names.join(', ')}` : 'DNS queries for every name';
}

/** The field of the form a problem the gateway reported names, by its pointer below the fault ("/names/0", "/delay"). */
export function dnsFieldOf(path: string | undefined): DnsField | undefined {
  const first = (path ?? '').split('/').filter(Boolean)[0];
  switch (first) {
    case 'action':
      return 'dnsAction';
    case 'names':
      return 'dnsNames';
    case 'delay':
      return 'dnsDelay';
    case 'answers':
      return 'dnsAnswers';
    case 'ttl':
      return 'dnsTtl';
    default:
      return undefined;
  }
}
