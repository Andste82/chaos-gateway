import { describe, expect, it } from 'vitest';
import type { RoutingProtocol } from '../api/generated/model';
import { CompleteSetupBody } from '../api/generated/zod/system/system';
import { protocolChanges } from './networkChanges';
import {
  emptyProtocolForm,
  formFromProtocol,
  formFromRouting,
  parsePrefixes,
  protocolFieldOf,
  protocolFormErrors,
  protocolFromForm,
  routingFromForm,
  routingSettingsErrors,
  routingSettingsFieldOf,
  seconds,
  type ProtocolContext,
} from './routingForm';

// The routing forms (P3-M14-01): the BIRD settings and a protocol on a WireGuard link, their checks
// under the fields, what they build (which the spec accepts) and the gateway's messages by field.

const LINK = 'c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f';
const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const ctx: ProtocolContext = { links: [{ id: LINK, name: 'site-b', address: '10.255.0.0/31' }], others: [{ name: 'site-b-bgp', type: 'bgp', link: LINK }] };

function acceptedBySpec(routing: unknown) {
  const configuration = { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth0' } }, routing };
  return CompleteSetupBody.safeParse({ admin_password: 'correct horse battery', configuration }).error;
}

describe('the routing forms (P3-M14-01)', () => {
  it('reads durations and prefix filters', () => {
    expect(seconds('1m30s')).toBe(90);
    expect(seconds('soon')).toBeUndefined();
    expect(parsePrefixes('10.70.0.0/16 le 24\n10.71.0.0/24')).toEqual({ entries: [{ prefix: '10.70.0.0/16', max_length: 24 }, { prefix: '10.71.0.0/24' }] });
    expect(parsePrefixes('10.70.0.0/16 up to 24').bad).toBe('10.70.0.0/16 up to 24');
  });

  it('an OSPF protocol with announcements and an import filter: no problem, and the protocol the spec accepts', () => {
    const f = {
      ...emptyProtocolForm(LINK),
      name: 'site-b-ospf',
      type: 'ospf' as const,
      area: '0.0.0.1',
      cost: '20',
      announce: [`network:${IOT}`],
      announceCidrs: '10.80.0.0/24',
      prefixes: '10.70.0.0/16 le 24',
      maxPrefixes: '50',
    };
    expect(protocolFormErrors(f, ctx)).toEqual({});
    const p = protocolFromForm(f);
    expect(p).toEqual({
      name: 'site-b-ospf',
      type: 'ospf',
      link: LINK,
      announce: [{ network: IOT }, { cidr: '10.80.0.0/24' }],
      import: { allowed_prefixes: [{ prefix: '10.70.0.0/16', max_length: 24 }], max_prefixes: 50 },
      ospf: { area: '0.0.0.1', cost: 20 },
    });
    expect(acceptedBySpec({ protocols: { 'b9000000-0000-4000-8000-000000000002': p } })).toBeUndefined();
  });

  it('says under each field what is wrong', () => {
    const bgp = { ...emptyProtocolForm(LINK), name: 'site-b-bgp', neighborAsn: '' };
    const e = protocolFormErrors(bgp, ctx);
    expect(e.name).toBe('A protocol is already called site-b-bgp.');
    expect(e.type).toBe('site-b already runs a BGP protocol (site-b-bgp).');
    expect(e.neighborAsn).toBe("The neighbor's AS number is required.");
    const other = { ...bgp, name: 'b2', link: 'x', neighborAsn: '65002', holdTime: '60s', keepaliveTime: '30s', neighborAddress: '10.255.0.7', prefixes: '10.70.0.0/16 le 8' };
    expect(protocolFormErrors(other, ctx)).toMatchObject({ link: 'That is no WireGuard link of the configuration.', holdTime: 'The hold time must be at least three keepalive intervals (90 s).', prefixes: expect.stringMatching(/from \/16 to \/32/) });
    expect(protocolFormErrors({ ...other, link: LINK }, { ...ctx, others: [] }).neighborAddress).toMatch(/peer in the link's transfer network 10.255.0.0\/31/);
    const ospf = { ...emptyProtocolForm(LINK), name: 'o', type: 'ospf' as const, helloInterval: '40s', area: 'backbone' };
    expect(protocolFormErrors(ospf, ctx)).toEqual({ area: 'An area such as 0 or 0.0.0.0.', deadInterval: 'The dead interval must be longer than the hello interval (40 s).' });
    expect(protocolFormErrors({ ...emptyProtocolForm(''), name: 'x' }, { links: [], others: [] }).link).toMatch(/add a link network first/);
  });

  it('opens a protocol again as its form and keeps what the form does not edit; another type drops the old settings', () => {
    const p: RoutingProtocol = { name: 'site-b-bgp', type: 'bgp', link: LINK, enabled: true, bgp: { neighbor_asn: 65002, hold_time: '9s', keepalive_time: '3s' }, import: { allow_default: true } };
    expect(protocolFromForm(formFromProtocol(p), p)).toEqual(p);
    const babel = protocolFromForm({ ...formFromProtocol(p), type: 'babel', rxcost: '128' }, p);
    expect(babel.bgp).toBeUndefined();
    expect(babel.babel).toEqual({ rxcost: 128 });
    expect(protocolChanges(p, { ...p, enabled: false })).toEqual([{ field: 'enabled', from: 'yes', to: 'no' }]);
  });

  it('the BIRD settings: router id, AS and the external mode with its filter', () => {
    const f = { ...formFromRouting({ protocols: {} }), asn: '65001', routerId: '10.10.0.1', externalEnabled: true, externalTable: '200', externalPrefixes: '10.80.0.0/16' };
    expect(routingSettingsErrors(f, true)).toEqual({});
    const r = routingFromForm(f, { protocols: {} });
    expect(r).toEqual({ protocols: {}, router_id: '10.10.0.1', asn: 65001, external: { enabled: true, table: 200, import: { allowed_prefixes: [{ prefix: '10.80.0.0/16' }] } } });
    expect(acceptedBySpec(r)).toBeUndefined();
    expect(routingSettingsErrors({ ...f, asn: '' }, true).asn).toBe('BGP needs the local AS number.');
    expect(routingSettingsErrors({ ...f, externalTable: '100' }, false).externalTable).toBe('Table 100 belongs to the system or to Chaos Gateway.');
    expect(routingSettingsErrors({ ...f, routerId: '224.0.0.1' }, false).routerId).toMatch(/unicast/);
    // an external mode never set and off stays out
    expect(routingFromForm(formFromRouting({ asn: 65001 }), { asn: 65001 })).toEqual({ asn: 65001 });
  });

  it("puts the gateway's messages under the field they are about", () => {
    expect(protocolFieldOf('bgp/neighbor_asn')).toBe('neighborAsn');
    expect(protocolFieldOf('bgp/hold_time')).toBe('holdTime');
    expect(protocolFieldOf('ospf/dead_interval')).toBe('deadInterval');
    expect(protocolFieldOf('import/allowed_prefixes/0/max_length')).toBe('prefixes');
    expect(protocolFieldOf('announce/1/cidr')).toBe('announceCidrs');
    expect(protocolFieldOf('protocol')).toBeUndefined();
    expect(routingSettingsFieldOf('asn')).toBe('asn');
    expect(routingSettingsFieldOf('external/table')).toBe('externalTable');
  });
});
