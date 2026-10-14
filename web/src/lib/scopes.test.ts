import { describe, expect, it } from 'vitest';
import type { NetworkView } from '../api/generated/model';
import { emptyForm, faultFormErrors, fieldOf, formToOverlay } from './faultForm';
import { CIDR_CHOICE, remoteCidrError, remoteScopeOptions, scopeChoice } from './scopes';

const HUB = '4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54';
const LINK = 'c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f';
const RA = '9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';
const RB = '9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4e';

const networks: NetworkView[] = [
  { id: 'iot', config: { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24' }, status: { state: 'ok' } },
  {
    id: HUB,
    config: {
      type: 'wireguard',
      kind: 'hub',
      name: 'lab-hub',
      address: '10.99.0.1/24',
      listen_port: 51820,
      clients: { [RA]: { name: 'rA', address: '10.99.0.2', client_networks: ['10.50.0.0/24'] }, [RB]: { name: 'rB', address: '10.99.0.3' } },
    },
    status: { state: 'ok' },
  },
  {
    id: LINK,
    config: { type: 'wireguard', kind: 'link', name: 'site-b', address: '10.255.0.0/31', listen_port: 51821, routes: ['10.60.0.0/24'], peer: { address: '10.255.0.1' } },
    status: { state: 'ok', link_peer: { online: true, learned_routes: ['10.70.0.0/24', '10.60.0.0/24'] } },
  },
];

describe('remote networks in the scope selectors (P3-M13-08)', () => {
  it('offers the clients with client networks, the links with their prefixes and a prefix inside them', () => {
    expect(remoteScopeOptions(networks)).toEqual([
      { value: `remote_network:client:${RA}`, label: 'Client networks · rA (10.50.0.0/24)' },
      { value: `remote_network:link:${LINK}`, label: 'Remote networks · link site-b (10.60.0.0/24, 10.70.0.0/24)' },
      { value: CIDR_CHOICE, label: 'Remote network · a prefix inside them…' },
    ]);
    // no WireGuard network: no remote choice at all
    expect(remoteScopeOptions(networks.slice(0, 1))).toEqual([]);
  });

  it('a prefix target is the CIDR choice with its prefix, sent as remote_network.cidr', () => {
    expect(scopeChoice('remote_network:cidr:10.50.0.0/25')).toBe(CIDR_CHOICE);
    expect(scopeChoice(`remote_network:client:${RA}`)).toBe(`remote_network:client:${RA}`);
    const f = { ...emptyForm('remote_network:cidr:10.50.0.0/25'), latency: '100' };
    expect(formToOverlay(f).target).toEqual({ remote_network: { cidr: '10.50.0.0/25' } });
    expect(faultFormErrors(f).target).toBeUndefined();
    expect(formToOverlay({ ...f, target: `remote_network:client:${RA}` }).target).toEqual({ remote_network: { client: RA } });
  });

  it("checks the prefix under the target, and puts the gateway's refusal of it there too", () => {
    expect(remoteCidrError(CIDR_CHOICE)).toMatch(/^Enter a prefix inside a client network/);
    expect(remoteCidrError('remote_network:cidr:10.50.0/25')).toBe('An IPv4 address or a prefix like 10.50.0.0/25.');
    expect(remoteCidrError('network:iot')).toBeUndefined();
    expect(faultFormErrors({ ...emptyForm(CIDR_CHOICE), latency: '100' }).target).toMatch(/^Enter a prefix/);
    expect(fieldOf('/target/remote_network/cidr')).toBe('target');
    expect(fieldOf('/faults/f1/source/remote_network/cidr')).toBe('target');
    expect(fieldOf('/fault/destination/cidr')).toBe('destination');
  });
});
