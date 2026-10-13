import { describe, expect, it } from 'vitest';
import type { AccessRule } from '../api/generated/model';
import { describeDestination, formatPorts, parsePorts, ruleEffectInWords, ruleErrors, ruleFieldChanges, ruleSummary } from './rules';

const names = { network: (id: string) => ({ n1: 'IoT', n2: 'Test' })[id] };
const rule: AccessRule = { name: 'no-dns', source: { network: 'n1' }, protocol: 'udp', ports: [53], action: 'drop' };

describe('access rules in words', () => {
  it('summarizes the selector', () => {
    expect(ruleSummary(rule, names)).toBe('Network IoT → any · UDP 53');
    expect(ruleSummary({ source: { network: 'n2' }, destination: { uplink: true }, action: 'allow' }, names)).toBe('Network Test → uplink · any protocol');
    expect(describeDestination({ network: 'n2' }, names)).toBe('network Test');
    expect(describeDestination({ hostname: 'broker.example.com' })).toBe('broker.example.com');
  });

  it('reads and writes ports and ranges', () => {
    expect(parsePorts('80, 443 1000-2000')).toEqual({ ports: [80, 443], port_ranges: [{ from: 1000, to: 2000 }] });
    expect(parsePorts('')).toEqual({});
    expect(parsePorts('0').error).toMatch(/is no port/);
    expect(parsePorts('20-10').error).toMatch(/is no range/);
    expect(parsePorts('http').error).toMatch(/http is no port/);
    expect(formatPorts({ ports: [80, 443], port_ranges: [{ from: 1000, to: 2000 }] })).toBe('80, 443, 1000-2000');
  });

  it('lists the fields that changed, old → new', () => {
    const after: AccessRule = { ...rule, action: 'reject', reject_with: 'admin_prohibited', ports: [53, 5353], enabled: false };
    expect(ruleFieldChanges(rule, after, names)).toEqual([
      { field: 'ports', from: '53', to: '53, 5353' },
      { field: 'access', from: 'Drop', to: 'Reject (administratively prohibited)' },
      { field: 'enabled', from: 'yes', to: 'no' },
    ]);
    expect(ruleFieldChanges(rule, { ...rule, source: { network: 'n2' } }, names)).toEqual([{ field: 'from', from: 'Network IoT', to: 'Network Test' }]);
  });

  it('says what the rule does with its traffic', () => {
    expect(ruleEffectInWords(rule)).toBe('Dropped silently for new connections; existing connections continue.');
    expect(ruleEffectInWords({ ...rule, action: 'reset', cut_existing: true }, { overlayRules: 2 })).toBe(
      'Refused with a TCP reset for new connections; existing connections are cut once. 2 overlay rules are checked first.',
    );
    expect(ruleEffectInWords({ ...rule, enabled: false })).toMatch(/^Disabled: matching traffic falls through/);
    expect(ruleEffectInWords({ ...rule, action: 'allow' })).toMatch(/^Allowed: new connections that match are accepted/);
    expect(ruleEffectInWords({ ...rule, destination: { hostname: 'x.example.com' } })).toMatch(/from milestone M20 on/);
  });

  it('checks a rule before it can be applied', () => {
    expect(ruleErrors(rule)).toEqual({});
    expect(ruleErrors({ ...rule, name: '-x' }).name).toBeTruthy();
    expect(ruleErrors({ ...rule, protocol: 'any' }).ports).toBe('Ports need the protocol TCP or UDP.');
    expect(ruleErrors(rule, '53, x').ports).toMatch(/x is no port/);
    expect(ruleErrors({ ...rule, protocol: 'tcp', action: 'allow', cut_existing: true }).cut_existing).toMatch(/Only a rule that refuses/);
    expect(ruleErrors({ ...rule, cut_existing: true }).cut_existing).toBe('Cutting connections needs the protocol TCP or any.');
    expect(ruleErrors({ ...rule, destination: { cidr: '10.0.0' } }).destination).toMatch(/IPv4 address or a prefix/);
    expect(ruleErrors({ ...rule, destination: { hostname: 'bad host' } }).destination).toMatch(/host name/);
  });
});
