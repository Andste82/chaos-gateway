import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import type { Configuration, RevisionWithConfiguration } from '../api/generated/model';
import { useDraftStore } from './draft';

// The networks and the routing in the configuration draft (P3-M14-01): their changes in words, their
// merge patch, the gateway's messages about them, and the concurrent-change dialog's reapply.

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const HUB = '4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54';
const LINK = 'c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f';
const CAM = 'e1000000-0000-4000-8000-000000000001';
const RA = '9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';
const RC = 'c1000000-0000-4000-8000-000000000003';
const BGP = 'b9000000-0000-4000-8000-000000000001';
const OSPF = 'b9000000-0000-4000-8000-000000000002';

const lan = { type: 'lan' as const, name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24' };
const hub = { type: 'wireguard' as const, kind: 'hub' as const, name: 'lab-hub', address: '10.99.0.1/24', listen_port: 51820 };
const link = { type: 'wireguard' as const, kind: 'link' as const, name: 'site-b', address: '10.255.0.0/31', listen_port: 51821, peer: { address: '10.255.0.1' } };
const names = { network: (id: string) => ({ [IOT]: 'IoT', [LINK]: 'site-b', [HUB]: 'lab-hub' })[id] };

function config() {
  return {
    schema_version: 1 as const,
    uplink: { interface: { name: 'eth0' } },
    management: { interface: { name: 'eth1' } },
    networks: { [IOT]: { ...lan }, [HUB]: { ...hub, clients: { [RA]: { name: 'rA', address: '10.99.0.2' } } }, [LINK]: { ...link } },
    routing: { asn: 65001, protocols: { [BGP]: { name: 'site-b-bgp', type: 'bgp' as const, link: LINK, bgp: { neighbor_asn: 65002 } } } },
  };
}

function revision(id: number, configuration: Configuration = config()): RevisionWithConfiguration {
  return { id, status: 'active', schema_version: 1, created_at: '', created_by: { type: 'user', id: 'admin' }, configuration };
}

beforeEach(() => setActivePinia(createPinia()));

describe('the networks and the routing in the draft (P3-M14-01)', () => {
  it('a new test network with several ports is a change in words and the whole network in the patch; taken back it is none', () => {
    const d = useDraftStore();
    d.track(revision(42));
    const cams = { type: 'lan' as const, name: 'cams', interfaces: [{ name: 'eth3', mac: '52:54:00:00:00:04' }, { name: 'eth4' }], address: '10.30.0.1/24' };
    d.setNetwork(CAM, cams);
    expect(d.changes()).toEqual([{ kind: 'network', id: CAM, name: 'cams', field: 'network', from: '—', to: 'added (test network 10.30.0.0/24, ports eth3, eth4)' }]);
    expect(d.summary()).toBe('1 change in 1 network');
    expect(d.patch()).toEqual({ networks: { [CAM]: cams } });
    expect(d.effectiveNetworks[CAM]).toEqual(cams);
    d.setNetwork(CAM, null);
    expect(d.dirty).toBe(false);
  });

  it("a hub edited keeps its clients out of the network's patch; a client added to it joins the same network entry", () => {
    const d = useDraftStore();
    d.track(revision(42));
    // the form gives back the hub with its clients: they are not part of the network's edit
    d.setNetwork(HUB, { ...hub, listen_port: 51830, role: 'management', clients: { [RA]: { name: 'rA', address: '10.99.0.2' } } });
    d.setClient(HUB, RC, { name: 'rC', address: '10.99.0.3' });
    expect(d.changes(names)).toEqual([
      { kind: 'client', id: RC, name: 'rC', field: 'client', from: '—', to: 'added (10.99.0.3 in lab-hub)' },
      { kind: 'network', id: HUB, name: 'lab-hub', field: 'role', from: 'test', to: 'management' },
      { kind: 'network', id: HUB, name: 'lab-hub', field: 'listen port', from: '51820', to: '51830' },
    ]);
    expect(d.patch()).toEqual({ networks: { [HUB]: { listen_port: 51830, role: 'management', clients: { [RC]: { name: 'rC', address: '10.99.0.3' } } } } });
    expect(Object.keys((d.effectiveNetworks[HUB] as { clients: object }).clients)).toEqual([RA]);
    // the settings back as they were: the client stays
    d.restoreNetwork(HUB);
    expect(d.patch()).toEqual({ networks: { [HUB]: { clients: { [RC]: { name: 'rC', address: '10.99.0.3' } } } } });
  });

  it('a network deleted is null in the patch and takes the edits of its clients along; Undo brings it back', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setClient(HUB, RC, { name: 'rC', address: '10.99.0.3' });
    d.setNetwork(HUB, null);
    expect(d.changes()).toEqual([{ kind: 'network', id: HUB, name: 'lab-hub', field: 'network', from: 'present', to: 'deleted' }]);
    expect(d.patch()).toEqual({ networks: { [HUB]: null } });
    expect(d.effectiveNetworks[HUB]).toBeUndefined();
    d.restoreNetwork(HUB);
    expect(d.dirty).toBe(false);
  });

  it("the gateway's messages about a network, a protocol and the routing settings keep the pointer below them", () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setServerErrors([
      { path: `/networks/${CAM}/dhcp/pools/0/end`, message: 'the end is before the start' },
      { path: `/networks/${CAM}`, message: 'not valid' },
      { path: `/routing/protocols/${BGP}/bgp/neighbor_asn`, message: 'too big' },
      { path: '/routing/asn', message: 'BGP needs the local AS number' },
    ]);
    expect(d.serverErrors).toEqual({
      [CAM]: { 'dhcp/pools/0/end': 'the end is before the start', network: 'not valid' },
      [BGP]: { 'bgp/neighbor_asn': 'too big' },
      routing: { asn: 'BGP needs the local AS number' },
    });
    // an edit of the routing clears its messages
    d.setProtocol(BGP, { name: 'site-b-bgp', type: 'bgp', link: LINK, bgp: { neighbor_asn: 65003 } });
    expect(d.serverErrors).toEqual({ [CAM]: { 'dhcp/pools/0/end': 'the end is before the start', network: 'not valid' } });
  });

  it('a protocol added, changed or deleted and the BIRD settings are changes of the routing and its merge patch', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setProtocol(OSPF, { name: 'site-b-ospf', type: 'ospf', link: LINK, ospf: { area: '0.0.0.1' }, announce: [{ network: IOT }] });
    d.setProtocol(BGP, { name: 'site-b-bgp', type: 'bgp', link: LINK, bgp: { neighbor_asn: 65003 } });
    d.setRouting({ ...d.effectiveRouting, asn: 65010 });
    expect(d.changes(names)).toEqual([
      { kind: 'routing', id: 'routing', name: 'settings', field: 'AS', from: '65001', to: '65010' },
      { kind: 'routing', id: BGP, name: 'protocol site-b-bgp', field: 'neighbor AS', from: '65002', to: '65003' },
      { kind: 'routing', id: OSPF, name: 'protocol site-b-ospf', field: '', from: '—', to: 'added (OSPF on site-b)' },
    ]);
    expect(d.summary(names)).toBe('3 changes in the routing');
    expect(d.patch()).toEqual({
      routing: {
        asn: 65010,
        protocols: { [BGP]: { bgp: { neighbor_asn: 65003 } }, [OSPF]: { name: 'site-b-ospf', type: 'ospf', link: LINK, ospf: { area: '0.0.0.1' }, announce: [{ network: IOT }] } },
      },
    });
    d.setProtocol(BGP, null);
    expect(d.patch()).toMatchObject({ routing: { protocols: { [BGP]: null } } });
    // each undone: the routing as it is
    d.restoreProtocol(BGP);
    d.setProtocol(OSPF, null);
    d.setRouting({ ...d.effectiveRouting, asn: 65001 });
    expect(d.dirty).toBe(false);
    expect(d.routing).toBeUndefined();
  });

  it('the first routing of a configuration without one is the whole routing in the patch', () => {
    const d = useDraftStore();
    const { routing: _none, ...bare } = config();
    d.track(revision(42, bare));
    d.setRouting({ asn: 65001 });
    expect(d.patch()).toEqual({ routing: { asn: 65001 } });
    d.setRouting({});
    expect(d.dirty).toBe(false);
  });

  it('reapplies networks and the routing on their revision: what only they changed stays theirs, a field both changed is named and mine', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setNetwork(IOT, { ...lan, interfaces: [{ name: 'eth1' }, { name: 'eth3' }], address: '10.11.0.1/24' });
    d.setProtocol(OSPF, { name: 'site-b-ospf', type: 'ospf', link: LINK });
    const theirs: Configuration = config();
    theirs.networks![IOT] = { ...lan, address: '10.12.0.1/24', dhcp: { lease_time: '2h' } };
    theirs.routing!.asn = 65020;
    expect(d.overlaps(theirs)).toEqual([{ kind: 'network', id: IOT, name: 'IoT', field: 'address', from: '10.12.0.1/24', to: '10.11.0.1/24' }]);
    d.rebase(revision(43, theirs));
    expect(d.effectiveNetworks[IOT]).toEqual({ ...lan, interfaces: [{ name: 'eth1' }, { name: 'eth3' }], address: '10.11.0.1/24', dhcp: { lease_time: '2h' } });
    expect(d.effectiveRouting.asn).toBe(65020);
    expect(Object.keys(d.effectiveRouting.protocols ?? {})).toEqual([BGP, OSPF]);
    expect(d.patch()).toEqual({
      networks: { [IOT]: { interfaces: [{ name: 'eth1' }, { name: 'eth3' }], address: '10.11.0.1/24' } },
      routing: { protocols: { [OSPF]: { name: 'site-b-ospf', type: 'ospf', link: LINK } } },
    });

    // a protocol both changed is named by its name
    d.discard();
    d.track(revision(44));
    d.setProtocol(BGP, { name: 'site-b-bgp', type: 'bgp', link: LINK, bgp: { neighbor_asn: 65003 } });
    const other = config();
    other.routing.protocols[BGP]!.bgp.neighbor_asn = 65004;
    expect(d.overlaps(other)).toEqual([{ kind: 'routing', id: 'routing', name: 'routing', field: 'protocol site-b-bgp bgp neighbor asn', from: '65004', to: '65003' }]);
  });
});
