// The form of a TLS case (plan §2.8, §2.17 "Add fault", M23b): the case the TLS responder plays (a certificate that must be
// rejected, or a handshake that fails), the connections it selects (ports, a destination, the server names of the
// ClientHello, whether connections that exist are cut) and the `tls` part of the request it becomes. The "Add fault"
// dialog (an overlay of the kind `tls`) and the step dialog (the step type `tls`) use the same fields, the same checks and
// the same body.
//
// The interception case (`intercept`, M22) has settings and a list of HTTP rules that this form does not edit: a case that
// has them is opened with them and sends them back as they were (the form of the rules is M23c); the choice of cases for a
// new case is the eight of the responder.
import { API_BASE } from '../api/fetcher';
import type { InterceptSettings, TlsCase } from '../api/generated/model';
import { createOverlayBodyTlsTwoSniItemRegExp as sniRegExp } from '../api/generated/zod/overlays/overlays';
import { trafficErrors, trafficFromMatch, trafficMatch, type TrafficField, type TrafficForm } from './trafficForm';

/** `GET /tls/ca`: the test CA certificate, a download. */
export const testCaUrl = `${API_BASE}/tls/ca`;

export type TlsCaseId = TlsCase['case'];

export interface TlsForm {
  case: TlsCaseId;
  /** The ports and the destination; the protocol is not a field of the case (TLS runs over TCP): it stays as the case had it, `any` (left out) for a new one. */
  traffic: TrafficForm;
  /** Server names of the ClientHello (exact or `*.suffix`), separated by commas, spaces or lines; empty is every connection. */
  sni: string;
  cutExisting: boolean;
  /** The settings of an interception case, kept as they are. */
  intercept?: InterceptSettings;
}

export type TlsField = 'tlsCase' | 'tlsSni' | 'tlsCut' | TrafficField;

export interface TlsCaseInfo {
  value: TlsCaseId;
  label: string;
  group: 'Certificate' | 'Handshake';
  hint: string;
  /** The case is signed by the test CA: only a device that trusts it can tell it from an unknown issuer. */
  testCa?: boolean;
}

export const tlsCases: TlsCaseInfo[] = [
  { value: 'untrusted_ca', label: 'Untrusted CA', group: 'Certificate', hint: 'A valid certificate for the server name, signed by the unknown CA that is never distributed. Any device must reject it.' },
  { value: 'self_signed', label: 'Self-signed', group: 'Certificate', hint: 'A certificate that is its own issuer. Any device must reject it.' },
  { value: 'expired', label: 'Expired', group: 'Certificate', testCa: true, hint: 'A certificate that ran out a day ago, signed by the test CA.' },
  { value: 'not_yet_valid', label: 'Not yet valid', group: 'Certificate', testCa: true, hint: 'A certificate that becomes valid in two days, signed by the test CA.' },
  { value: 'hostname_mismatch', label: 'Hostname mismatch', group: 'Certificate', testCa: true, hint: 'A valid certificate of the test CA for another name than the one the device asked for.' },
  { value: 'handshake_reset', label: 'Handshake reset', group: 'Handshake', hint: 'The connection is reset when the device starts the handshake.' },
  { value: 'handshake_close', label: 'Handshake closed', group: 'Handshake', hint: 'The connection is closed in an orderly way when the device starts the handshake.' },
  { value: 'handshake_stall', label: 'Handshake stalled', group: 'Handshake', hint: 'The handshake is never answered: the device waits for its own timeout.' },
];

/** The label of the interception case, which a case being edited can have (it is not offered for a new one). */
export const interceptLabel = 'Interception · its settings and rules are kept as they are';

export function emptyTls(): TlsForm {
  return { case: 'untrusted_ca', traffic: { protocol: 'any', ports: '', destKind: 'any', destination: '' }, sni: '', cutExisting: false };
}

/** Words or lines as typed: "a, b c" → ["a", "b", "c"]. */
function list(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/** The problems of the form, by field; empty when the case can be sent. */
export function tlsErrors(f: TlsForm): Partial<Record<TlsField, string>> {
  // the ports of a TLS case need no protocol: the protocol is TCP
  const e: Partial<Record<TlsField, string>> = { ...trafficErrors({ ...f.traffic, protocol: 'tcp' }) };
  const names = list(f.sni);
  const bad = names.find((n) => !sniRegExp.test(n));
  if (bad) e.tlsSni = `${bad} is no server name: a name like broker.example.com, or *.example.com.`;
  else if (names.some((n, i) => names.findIndex((m) => m.toLowerCase() === n.toLowerCase()) !== i)) e.tlsSni = 'Each name once.';
  return e;
}

/** The TLS case of the form. Without ports the responder selects 443 and 8883. */
export function tlsBody(f: TlsForm): TlsCase {
  const out: TlsCase = { case: f.case, ...trafficMatch(f.traffic) };
  const names = list(f.sni);
  if (names.length) out.sni = names;
  if (f.cutExisting) out.cut_existing = true;
  if (f.case === 'intercept' && f.intercept) out.intercept = f.intercept;
  return out;
}

/** The form of a TLS case. */
export function tlsToForm(t: TlsCase | undefined): TlsForm {
  const f = emptyTls();
  if (!t) return f;
  f.case = t.case;
  f.traffic = { ...trafficFromMatch(t), protocol: t.protocol ?? 'any' };
  f.sni = (t.sni ?? []).join(', ');
  f.cutExisting = t.cut_existing === true;
  if (t.intercept) f.intercept = t.intercept;
  return f;
}

/** The ports the case selects, in words: "ports 443, 8883 (the default)", "ports 8000-8100". */
function portsText(f: TlsForm): string {
  const body = trafficMatch(f.traffic);
  const ports = [...(body.ports ?? []).map(String), ...(body.port_ranges ?? []).map((r) => `${r.from}-${r.to}`)];
  return ports.length ? `TCP ${ports.join(', ')}` : 'TCP 443, 8883';
}

/** What the case does, in words: "an expired certificate", "a reset handshake". */
export function tlsEffect(c: TlsCaseId): string {
  switch (c) {
    case 'untrusted_ca':
      return 'a certificate of an unknown CA';
    case 'self_signed':
      return 'a self-signed certificate';
    case 'expired':
      return 'an expired certificate';
    case 'not_yet_valid':
      return 'a certificate that is not valid yet';
    case 'hostname_mismatch':
      return 'a certificate for another host name';
    case 'handshake_reset':
      return 'a reset handshake';
    case 'handshake_close':
      return 'a closed handshake';
    case 'handshake_stall':
      return 'a stalled handshake';
    case 'intercept':
      return 'the interception proxy';
    default:
      return String(c);
  }
}

/** The connections the form selects, in words: "TLS connections on TCP 443, 8883 to 203.0.113.0/24 for broker.example.com". */
export function tlsScope(f: TlsForm, destination: string): string {
  const names = list(f.sni);
  return `TLS connections on ${portsText(f)}${destination ? ` ${destination}` : ''}${names.length ? ` for ${names.join(', ')}` : ''}${f.cutExisting ? ', existing ones cut' : ''}`;
}

/** The field of the form a problem the gateway reported names, by its pointer below the case ("/ports/0", "/sni/1", "/destination/cidr"). */
export function tlsFieldOf(path: string | undefined): TlsField | undefined {
  const segments = (path ?? '').split('/').filter(Boolean);
  switch (segments[0]) {
    case 'case':
      return 'tlsCase';
    case 'sni':
      return 'tlsSni';
    case 'cut_existing':
      return 'tlsCut';
    case 'ports':
    case 'port_ranges':
    case 'protocol':
      return 'ports';
    case 'destination':
      return 'destination';
    default:
      return undefined;
  }
}
