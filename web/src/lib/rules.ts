// Access rules in the user's words (plan §2.4, §2.17 "Access rules"): the selector as one line, the
// destination and the ports as the editor shows them, what changed in a rule, what the rule does
// with the traffic it selects, and the checks the editor makes before a change can be applied.
import type { AccessAction, AccessRule, Destination, PortRange, Protocol, RejectType, TrafficMatch } from '../api/generated/model';
import { createOverlayBodyFaultOneDestinationCidrRegExp as cidrRegExp, createOverlayBodyFaultOneDestinationHostnameOneRegExp as hostnameRegExp } from '../api/generated/zod/overlays/overlays';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { describeScope, type Names } from './faults';
import { jsonEqual } from './mergePatch';
import { CIDR_CHOICE, remoteCidrError } from './scopes';

export const accessLabels: Record<AccessAction, string> = { allow: 'Allow', drop: 'Drop', reject: 'Reject', reset: 'TCP reset' };

export const rejectLabels: Record<RejectType, string> = {
  port_unreachable: 'port unreachable',
  host_unreachable: 'host unreachable',
  admin_prohibited: 'administratively prohibited',
  no_route: 'no route',
};

/** The destination as the rule list shows it: "any", "uplink", "network IoT", "203.0.113.0/24". */
export function describeDestination(d: Destination | undefined, names: Names = {}): string {
  if (!d) return 'any';
  if (d.uplink) return 'uplink';
  if (d.network) return `network ${names.network?.(d.network) ?? d.network}`;
  if (d.cidr) return d.cidr;
  if (d.hostname) return d.hostname;
  return 'any';
}

/** The ports of a selector as one text: "80, 443, 1000-2000"; "" for any. */
export function formatPorts(m: TrafficMatch | undefined): string {
  if (!m) return '';
  return [...(m.ports ?? []).map(String), ...(m.port_ranges ?? []).map((r) => `${r.from}-${r.to}`)].join(', ');
}

export interface ParsedPorts {
  ports?: number[];
  port_ranges?: PortRange[];
  error?: string;
}

/** Reads "80, 443, 1000-2000" (commas or spaces); an empty text is any port. */
export function parsePorts(text: string): ParsedPorts {
  const parts = text
    .split(/[\s,]+/)
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length === 0) return {};
  const ports: number[] = [];
  const ranges: PortRange[] = [];
  const valid = (n: number) => Number.isInteger(n) && n >= 1 && n <= 65535;
  for (const p of parts) {
    const range = /^(\d+)-(\d+)$/.exec(p);
    if (range) {
      const from = Number(range[1]);
      const to = Number(range[2]);
      if (!valid(from) || !valid(to) || from > to) return { error: `${p} is no range of ports from 1 to 65535.` };
      ranges.push({ from, to });
    } else if (/^\d+$/.test(p) && valid(Number(p))) {
      ports.push(Number(p));
    } else {
      return { error: `${p} is no port: a number from 1 to 65535, or a range like 1000-2000.` };
    }
  }
  if (ports.length > 64) return { error: 'At most 64 ports; use a range.' };
  if (ranges.length > 16) return { error: 'At most 16 ranges.' };
  return { ...(ports.length ? { ports } : {}), ...(ranges.length ? { port_ranges: ranges } : {}) };
}

/** "Network IoT → uplink · TCP 80, 443" */
export function ruleSummary(r: Partial<AccessRule>, names: Names = {}, source?: string): string {
  const from = source ?? describeScope(r.source, names);
  const proto = r.protocol && r.protocol !== 'any' ? r.protocol.toUpperCase() : 'any protocol';
  const ports = formatPorts(r);
  return `${from} → ${describeDestination(r.destination, names)} · ${proto}${ports ? ' ' + ports : ''}`;
}

/** The access chip's text: "Drop", "Reject · cut existing". */
export function accessLabel(r: { action: AccessAction; cut_existing?: boolean }): string {
  return accessLabels[r.action] + (r.cut_existing ? ' · cut existing' : '');
}

/** One change of a field of a rule, in the words of the preview: rule, field, old → new. */
export interface FieldChange {
  field: string;
  from: string;
  to: string;
}

function actionText(r: AccessRule): string {
  return r.action === 'reject' && r.reject_with ? `${accessLabels.reject} (${rejectLabels[r.reject_with]})` : accessLabels[r.action];
}

/** The fields that differ between two versions of a rule. */
export function ruleFieldChanges(before: AccessRule, after: AccessRule, names: Names = {}): FieldChange[] {
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => {
    if (a !== b) out.push({ field, from: a || '—', to: b || '—' });
  };
  add('name', before.name ?? '', after.name ?? '');
  add('description', before.description ?? '', after.description ?? '');
  if (!jsonEqual(before.source, after.source)) add('from', describeScope(before.source, names), describeScope(after.source, names));
  if (!jsonEqual(before.destination, after.destination))
    add('to', describeDestination(before.destination, names), describeDestination(after.destination, names));
  add('protocol', before.protocol ?? 'any', after.protocol ?? 'any');
  add('ports', formatPorts(before) || 'any', formatPorts(after) || 'any');
  add('access', actionText(before), actionText(after));
  add('cut existing', before.cut_existing ? 'yes' : 'no', after.cut_existing ? 'yes' : 'no');
  add('enabled', before.enabled === false ? 'no' : 'yes', after.enabled === false ? 'no' : 'yes');
  return out;
}

/**
 * What the rule does with the traffic it selects, in words (the editor's matches box). `state` is
 * the gateway's: an enabled rule it does not run (a change the gateway has not applied yet) says so.
 */
export function ruleEffectInWords(r: AccessRule, opts: { state?: string; overlayRules?: number; applied?: boolean } = {}): string {
  if (r.enabled === false) return 'Disabled: matching traffic falls through to the next rule.';
  const parts: string[] = [];
  if (opts.state === 'disabled' && opts.applied) {
    parts.push('Not in the packet path: the gateway does not run this rule.');
  }
  if (r.destination?.hostname) {
    parts.push("DNS-derived: it matches the addresses the gateway's DNS answered the device for the name, as long as it has them");
  }
  switch (r.action) {
    case 'allow':
      parts.push('Allowed: new connections that match are accepted, also where the access matrix would refuse them. Faults still apply.');
      break;
    case 'drop':
      parts.push('Dropped silently for new connections');
      break;
    case 'reject':
      parts.push(`Rejected for new connections with ICMP ${rejectLabels[r.reject_with ?? 'port_unreachable']}, so the sender fails at once`);
      break;
    case 'reset':
      parts.push('Refused with a TCP reset for new connections');
      break;
  }
  if (r.action !== 'allow') parts[parts.length - 1] += r.cut_existing ? '; existing connections are cut once.' : '; existing connections continue.';
  if (opts.overlayRules) parts.push(`${opts.overlayRules} overlay ${opts.overlayRules === 1 ? 'rule is' : 'rules are'} checked first.`);
  return parts.join(' ');
}

/** The gateway's `reset_requires_tcp`: a reset is a TCP segment, so the rule must select TCP alone (the protocol `any` is not enough). */
export function resetError(action: string | undefined, protocol: string | undefined): string | undefined {
  return action === 'reset' && protocol !== 'tcp' ? 'A TCP reset needs the protocol TCP.' : undefined;
}

export type RuleField = 'name' | 'destination' | 'ports' | 'action' | 'cut_existing' | 'source';

/** The problems of a rule, by field (plan §2.17 "Validation": the message under the field). */
export function ruleErrors(r: AccessRule, portsText?: string): Partial<Record<RuleField, string>> {
  const e: Partial<Record<RuleField, string>> = {};
  if (r.name && !nameRegExp.test(r.name)) e.name = 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.';
  if (!r.source || Object.keys(r.source).length !== 1) e.source = 'Choose where the traffic comes from.';
  else if (r.source.remote_network?.cidr !== undefined) e.source = remoteCidrError(`${CIDR_CHOICE}${r.source.remote_network.cidr}`);
  if (!e.source) delete e.source;
  const d = r.destination;
  if (d?.cidr !== undefined && !cidrRegExp.test(d.cidr)) e.destination = 'An IPv4 address or a prefix like 203.0.113.0/24.';
  if (d?.hostname !== undefined && !hostnameRegExp.test(d.hostname)) e.destination = 'A host name like broker.example.com or *.example.com.';
  if (d?.network !== undefined && !d.network) e.destination = 'Choose a network.';
  if (portsText !== undefined) {
    const p = parsePorts(portsText);
    if (p.error) e.ports = p.error;
  }
  if (!e.ports && (r.ports?.length || r.port_ranges?.length) && r.protocol !== 'tcp' && r.protocol !== 'udp') e.ports = 'Ports need the protocol TCP or UDP.';
  const reset = resetError(r.action, r.protocol);
  if (reset) e.action = reset;
  if (r.cut_existing && r.action === 'allow') e.cut_existing = 'Only a rule that refuses traffic can cut connections.';
  if (r.cut_existing && r.protocol && r.protocol !== 'tcp' && r.protocol !== 'any') e.cut_existing = 'Cutting connections needs the protocol TCP or any.';
  return e;
}

/** A new rule's defaults: everything to the uplink, dropped, enabled. */
export function newRule(name: string, source: AccessRule['source']): AccessRule {
  return { name, source, destination: { uplink: true }, protocol: 'any' as Protocol, action: 'drop', enabled: true };
}
