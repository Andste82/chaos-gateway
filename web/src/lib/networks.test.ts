import { describe, expect, it } from 'vitest';
import type { NetworkView } from '../api/generated/model';
import { CompleteSetupBody } from '../api/generated/zod/system/system';
import {
  cellOf,
  clientFieldOf,
  clientFormErrors,
  clientFromForm,
  effectivePools,
  emptyClientForm,
  formFromClient,
  matrixChanges,
  nextFreeAddress,
  parseSubnet,
  subnetText,
  toggleCell,
  formatIPv4,
  keysText,
  rotatedKeyError,
  withEnabled,
  withRotatedKeys,
} from './networks';

const IOT = '0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21';
const TEST = '2c9b8e7f-6a54-4e2f-9d1c-8b3a4f6ea1d7';
const HUB = '4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54';

const networks: NetworkView[] = [
  { id: IOT, config: { type: 'lan', name: 'IoT', interfaces: [{ name: 'eth1' }], address: '10.10.0.1/24' }, status: { state: 'ok' } },
  { id: TEST, config: { type: 'lan', name: 'Test', interfaces: [{ name: 'eth2' }], address: '10.20.0.1/24' }, status: { state: 'ok' } },
  { id: HUB, config: { type: 'wireguard', kind: 'hub', name: 'lab-hub', address: '10.99.0.1/24', listen_port: 51820 }, status: { state: 'ok' } },
];

describe('IPv4 of the networks', () => {
  it('reads a subnet with its network and broadcast address', () => {
    const s = parseSubnet('10.99.0.1/24')!;
    expect([formatIPv4(s.network), formatIPv4(s.broadcast), s.len]).toEqual(['10.99.0.0', '10.99.0.255', 24]);
    expect(subnetText('10.10.0.1/24')).toBe('10.10.0.0/24');
    expect(parseSubnet('10.99.0.1')).toBeUndefined();
    expect(parseSubnet('10.99.0.300/24')).toBeUndefined();
  });

  it('the default DHCP pool is the second half of the subnet, without the broadcast and the gateway address', () => {
    expect(effectivePools('10.10.0.1/24', undefined)).toEqual({ pools: [{ start: '10.10.0.128', end: '10.10.0.254' }], isDefault: true });
    expect(effectivePools('10.10.0.200/24', [])).toEqual({ pools: [{ start: '10.10.0.128', end: '10.10.0.254' }], isDefault: true });
    expect(effectivePools('10.10.0.254/24', undefined).pools[0]!.end).toBe('10.10.0.253');
    const own = [{ start: '10.10.0.100', end: '10.10.0.150' }];
    expect(effectivePools('10.10.0.1/24', own)).toEqual({ pools: own, isDefault: false });
  });
});

describe('the next free tunnel address of a hub (plan M14, computed client-side)', () => {
  it('is the lowest host address that is not the gateway and not taken', () => {
    expect(nextFreeAddress('10.99.0.1/24', [])).toBe('10.99.0.2');
    expect(nextFreeAddress('10.99.0.1/24', ['10.99.0.2', '10.99.0.4'])).toBe('10.99.0.3');
    // the gateway's address need not be the first one
    expect(nextFreeAddress('10.99.0.254/24', [])).toBe('10.99.0.1');
  });

  it('is none when the subnet is full, and a /30 has two hosts', () => {
    expect(nextFreeAddress('10.99.0.1/30', ['10.99.0.2'])).toBeUndefined();
    expect(nextFreeAddress('10.99.0.1/30', [])).toBe('10.99.0.2');
    expect(nextFreeAddress('nonsense', [])).toBeUndefined();
  });
});

describe('the add-client form', () => {
  const ctx = { hubAddress: '10.99.0.1/24', usedAddresses: ['10.99.0.2'], takenNames: ['ra', 'esp32-42'] };

  it('accepts a name and a free address of the hub', () => {
    expect(clientFormErrors({ ...emptyClientForm('10.99.0.3'), name: 'laptop' }, ctx)).toEqual({});
  });

  it('says under each field what is wrong', () => {
    const e = (over: Partial<ReturnType<typeof emptyClientForm>>) => clientFormErrors({ ...emptyClientForm('10.99.0.3'), name: 'laptop', ...over }, ctx);
    expect(e({ name: '' }).name).toMatch(/required/);
    expect(e({ name: '-bad' }).name).toMatch(/starts with a letter or digit/);
    expect(e({ name: 'rA' }).name).toMatch(/already called rA/);
    expect(e({ address: '10.98.0.3' }).address).toMatch(/hub's subnet 10.99.0.0\/24/);
    expect(e({ address: '10.99.0.2' }).address).toMatch(/Another client/);
    expect(e({ address: '10.99.0.1' }).address).toMatch(/gateway's own/);
    expect(e({ address: '10.99.0.255' }).address).toMatch(/broadcast/);
    expect(e({ address: 'x' }).address).toMatch(/IPv4 address/);
    expect(e({ clientNetworks: '10.50.0.0/24, 10.51.0.0' }).clientNetworks).toMatch(/10.51.0.0 is no IPv4 network/);
    expect(e({ clientNetworks: '10.99.0.0/25' }).clientNetworks).toMatch(/hub's own subnet/);
    expect(e({ dns: 'custom', dnsServers: '' }).dnsServers).toMatch(/at least one/);
    expect(e({ dns: 'custom', dnsServers: '1.1.1.1, x' }).dnsServers).toMatch(/IPv4 addresses/);
    expect(e({ keepalive: '25 seconds' }).keepalive).toMatch(/duration/);
    expect(e({ keyMode: 'provided', publicKey: 'short' }).publicKey).toMatch(/public key/);
    expect(e({ keyMode: 'provided', publicKey: 'kso2jlxO7Hjdde1KFPmTsqV7PJV47Pqi3agv3NqKxiY=' }).publicKey).toBeUndefined();
  });

  it('builds the client the configuration stores, which a candidate of the spec accepts', () => {
    const c = clientFromForm({
      ...emptyClientForm('10.99.0.3'),
      name: 'laptop',
      clientNetworks: '10.50.0.0/24 10.51.0.0/24',
      reachable: [`network:${IOT}`, 'uplink'],
      dns: 'custom',
      dnsServers: '1.1.1.1',
      keepalive: '10s',
      presharedKey: true,
      exportOnce: true,
    });
    expect(c).toEqual({
      name: 'laptop',
      address: '10.99.0.3',
      client_networks: ['10.50.0.0/24', '10.51.0.0/24'],
      reachable: [{ network: IOT }, { uplink: true }],
      dns: { mode: 'custom', servers: ['1.1.1.1'] },
      keepalive: '10s',
      key: { mode: 'generated', preshared_key: true, export_once: true },
    });
    // the defaults stay out: a client of name and address only
    expect(clientFromForm({ ...emptyClientForm('10.99.0.3'), name: 'phone' })).toEqual({ name: 'phone', address: '10.99.0.3', key: { mode: 'generated' } });
    const configuration = { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, networks: { [HUB]: { ...networks[2]!.config, clients: { 'c1000000-0000-4000-8000-000000000001': c } } } };
    expect(CompleteSetupBody.safeParse({ admin_password: 'correct horse battery', configuration }).error).toBeUndefined();
  });

  it('opens a client of the draft again as the form that made it', () => {
    const forms = [
      { ...emptyClientForm('10.99.0.3'), name: 'phone' },
      {
        ...emptyClientForm('10.99.0.4'),
        name: 'laptop',
        clientNetworks: '10.50.0.0/24, 10.51.0.0/24',
        reachable: [`network:${IOT}`, 'uplink'],
        dns: 'custom' as const,
        dnsServers: '1.1.1.1, 9.9.9.9',
        keepalive: '10s',
        presharedKey: true,
        exportOnce: true,
      },
      { ...emptyClientForm('10.99.0.5'), name: 'own-key', dns: 'none' as const, keyMode: 'provided' as const, publicKey: 'kso2jlxO7Hjdde1KFPmTsqV7PJV47Pqi3agv3NqKxiY=' },
    ];
    for (const f of forms) expect(formFromClient(clientFromForm(f))).toEqual(f);
  });

  it("puts the gateway's messages under the field they are about", () => {
    expect(clientFieldOf('address')).toBe('address');
    expect(clientFieldOf('client_networks')).toBe('clientNetworks');
    expect(clientFieldOf('key')).toBe('publicKey');
    expect(clientFieldOf('client')).toBeUndefined();
    expect(clientFieldOf('reachable')).toBeUndefined();
  });
});

describe('the access matrix', () => {
  it('without an entry a test network reaches the uplink, everything else is blocked', () => {
    expect(cellOf([], `network:${IOT}`, 'uplink', networks)).toEqual({ policy: 'allow', explicit: false });
    expect(cellOf([], `network:${HUB}`, 'uplink', networks)).toEqual({ policy: 'deny', explicit: false });
    expect(cellOf([], `network:${IOT}`, `network:${TEST}`, networks)).toEqual({ policy: 'deny', explicit: false });
    // an entry may name the network by its name
    expect(cellOf([{ from: { network: 'IoT' }, to: { network: 'lab-hub' }, policy: 'allow' }], `network:${IOT}`, `network:${HUB}`, networks)).toEqual({
      policy: 'allow',
      explicit: true,
    });
  });

  it('a click flips the cell; an entry that says what the default says is removed', () => {
    const blocked = toggleCell([], `network:${IOT}`, 'uplink', networks);
    expect(blocked).toEqual([{ from: { network: IOT }, to: { uplink: true }, policy: 'deny' }]);
    expect(toggleCell(blocked, `network:${IOT}`, 'uplink', networks)).toEqual([]);
    const byName = [{ from: { network: 'IoT' }, to: { management: true as const }, policy: 'allow' as const }];
    expect(toggleCell(byName, `network:${IOT}`, 'management', networks)).toEqual([]);
  });

  it('says the changes in words', () => {
    const names = { network: (id: string) => networks.find((n) => n.id === id)?.config.name };
    const after = toggleCell(toggleCell([], `network:${IOT}`, 'uplink', networks), `network:${TEST}`, `network:${HUB}`, networks);
    expect(matrixChanges([], after, networks, names)).toEqual([
      { field: 'IoT → Internet', from: 'allowed', to: 'blocked' },
      { field: 'Test → lab-hub', from: 'blocked', to: 'allowed' },
    ]);
  });
});

describe('the client actions (P3-M14-01)', () => {
  const KEY = 'qU1BFDFDgsH5I7uGhzuhQjkMSw6bP8nXN6CN4AKxEF8=';
  const NEW_KEY = 'FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=';

  it('disables a client with enabled false and enables it by leaving the default', () => {
    const c = { name: 'rA', address: '10.99.0.2', keepalive: '1s' };
    expect(withEnabled(c, false)).toEqual({ ...c, enabled: false });
    expect(withEnabled({ ...c, enabled: false }, true)).toEqual(c);
    // a client the configuration has with an explicit enabled: true keeps it, so an enable after a disable is no change
    expect(withEnabled({ ...c, enabled: false }, true, { ...c, enabled: true })).toEqual({ ...c, enabled: true });
    expect(withEnabled(c, false, { ...c, enabled: true })).toEqual({ ...c, enabled: false });
  });

  it('rotates generated keys by the next generation, checked against the spec', () => {
    const c = { name: 'rA', address: '10.99.0.2', key: { mode: 'generated' as const, preshared_key: true } };
    const r = withRotatedKeys(c);
    expect(r.key).toEqual({ mode: 'generated', preshared_key: true, generation: 2 });
    expect(withRotatedKeys(r).key?.generation).toBe(3);
    // a client without key settings has generated keys of generation 1
    expect(withRotatedKeys({ name: 'rB', address: '10.99.0.3' }).key).toEqual({ generation: 2 });
    expect(keysText(c)).toBe('generation 1');
    expect(keysText(r)).toBe('generation 2');
    const cfg = { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } } };
    const net = { type: 'wireguard', kind: 'hub', name: 'lab-hub', address: '10.99.0.1/24', listen_port: 51820, clients: { 'c1000000-0000-4000-8000-000000000001': r } };
    expect(CompleteSetupBody.safeParse({ admin_password: 'correct horse battery staple', configuration: { ...cfg, networks: { 'd1000000-0000-4000-8000-000000000001': net } } }).success).toBe(true);
  });

  it("rotates the client's own key by its new public key, which must be a key and a new one", () => {
    const c = { name: 'rP', address: '10.99.0.5', key: { mode: 'provided' as const, public_key: KEY } };
    expect(rotatedKeyError(c, 'abc')).toBe("The client's new WireGuard public key (44 characters of base64).");
    expect(rotatedKeyError(c, KEY)).toBe('This is the key the client has now.');
    expect(rotatedKeyError(c, ` ${NEW_KEY} `)).toBeUndefined();
    // no generation: the gateway refuses one for a client's own key
    expect(withRotatedKeys(c, NEW_KEY).key).toEqual({ mode: 'provided', public_key: NEW_KEY });
    expect(keysText(c)).toBe('public key qU1BFDFD…');
  });
});
