import { describe, expect, it } from 'vitest';
import type { Explanation, Flow } from '../api/generated/model';
import {
  destinationText,
  dnsAnswerText,
  dnsQueryTone,
  explainFromQuery,
  explainQuery,
  explainRequest,
  faultBadgeText,
  filterFlows,
  flowBytes,
  flowParams,
  flowsFiltered,
  noFlowFilters,
  pathState,
  pathTargets,
  serviceLabel,
  sortFlows,
  stateText,
} from './flows';

const flow = (o: Partial<Flow>): Flow => ({ id: 'tcp/10.10.0.42:1>1.1.1.1:2', protocol: 'tcp', src: '10.10.0.42', dst: '203.0.113.10', ...o });

describe('what a connection is called', () => {
  it('names the service of a well-known port, else the protocol', () => {
    expect(serviceLabel({ protocol: 'tcp', dport: 8883 })).toBe('MQTT/TLS');
    expect(serviceLabel({ protocol: 'udp', dport: 123 })).toBe('NTP');
    expect(serviceLabel({ protocol: 'udp', dport: 40000 })).toBe('UDP');
    expect(serviceLabel({ protocol: 'icmp' })).toBe('ICMP');
    expect(serviceLabel({ protocol: 'tcp', dport: 443, service: 'tls_responder' })).toBe('TLS test');
    expect(serviceLabel({ protocol: 'udp', dport: 5000, service: 'dns_proxy' })).toBe('DNS');
  });

  it('shows the name the DNS proxy returned for the address, with the port', () => {
    expect(destinationText({ dst: '203.0.113.10', dst_hostname: 'broker.example.com', dport: 8883 })).toBe('broker.example.com:8883');
    expect(destinationText({ dst: '203.0.113.10', dport: 443 })).toBe('203.0.113.10:443');
    expect(destinationText({ dst: '203.0.113.10' })).toBe('203.0.113.10');
  });

  it('says the state in words', () => {
    expect(stateText({ protocol: 'tcp', state: 'ESTABLISHED' })).toBe('established');
    expect(stateText({ protocol: 'tcp', state: 'SYN_SENT' })).toBe('syn sent');
    expect(stateText({ protocol: 'udp' })).toBe('udp');
    expect(stateText({ protocol: 'icmp' })).toBe('—');
  });

  it('adds the bytes of both directions', () => {
    expect(flowBytes(flow({ upload: { packets: 1, bytes: 412 }, download: { packets: 1, bytes: 500 } }))).toBe(912);
    expect(flowBytes(flow({}))).toBe(0);
  });
});

describe('the order and the filters of the flow view', () => {
  it('lists the newest connection first, those without a start after them, and keeps the order between polls', () => {
    const a = flow({ id: 'a', started_at: '2026-10-27T10:00:00Z' });
    const b = flow({ id: 'b', started_at: '2026-10-27T11:00:00Z' });
    const c = flow({ id: 'c' });
    const d = flow({ id: 'd' });
    expect(sortFlows([d, a, c, b]).map((f) => f.id)).toEqual(['b', 'a', 'c', 'd']);
    expect(sortFlows([c, d, b, a]).map((f) => f.id)).toEqual(['b', 'a', 'c', 'd']);
  });

  it('leaves device and network to the gateway and filters the rest here', () => {
    const f = { ...noFlowFilters(), device: 'dev', network: 'net', text: 'mqtt' };
    expect(flowParams(f, 500)).toEqual({ device: 'dev', network: 'net', limit: 500 });
    expect(flowParams(noFlowFilters(), 500)).toEqual({ limit: 500 });
    expect(flowsFiltered(noFlowFilters())).toBe(false);
    expect(flowsFiltered({ ...noFlowFilters(), text: ' x ' })).toBe(true);
    expect(flowsFiltered({ ...noFlowFilters(), text: '  ' })).toBe(false);
    expect(flowsFiltered({ ...noFlowFilters(), forwarded: true })).toBe(true);
  });

  it('selects by text in addresses, names, ports and services, by protocol and by port forward', () => {
    const mqtt = flow({ id: 'mqtt', dport: 8883, dst_hostname: 'broker.example.com' });
    const ntp = flow({ id: 'ntp', protocol: 'udp', dport: 123, dst: '192.0.2.123' });
    const fwd = flow({ id: 'fwd', src: '198.51.100.7', dst: '203.0.113.1', dport: 8080, forwarded: true });
    const all = [mqtt, ntp, fwd];
    const ids = (f: Partial<ReturnType<typeof noFlowFilters>>) => filterFlows(all, { ...noFlowFilters(), ...f }).map((x) => x.id);
    expect(ids({})).toEqual(['mqtt', 'ntp', 'fwd']);
    expect(ids({ text: 'BROKER' })).toEqual(['mqtt']);
    expect(ids({ text: '8883' })).toEqual(['mqtt']);
    expect(ids({ text: 'ntp' })).toEqual(['ntp']);
    expect(ids({ text: '192.0.2' })).toEqual(['ntp']);
    expect(ids({ protocol: 'udp' })).toEqual(['ntp']);
    expect(ids({ forwarded: true })).toEqual(['fwd']);
    expect(ids({ text: '198.51.100', forwarded: true })).toEqual(['fwd']);
    expect(ids({ text: 'nothing' })).toEqual([]);
  });
});

describe('explaining a connection', () => {
  it("asks for the device of a device's connection, with the port of TCP and UDP", () => {
    const r = explainRequest(flow({ device: 'dev-1', dport: 8883 }));
    expect(r).toEqual({ device: 'dev-1', dst: '203.0.113.10', protocol: 'tcp', port: 8883 });
    expect(explainRequest(flow({ device: 'dev-1', protocol: 'icmp' }))).toEqual({ device: 'dev-1', dst: '203.0.113.10', protocol: 'icmp' });
    expect(explainRequest(flow({ device: 'dev-1', protocol: 'other' }))).toEqual({ device: 'dev-1', dst: '203.0.113.10' });
  });

  it('asks for the source address of a forwarded connection and of a host without a device', () => {
    const fwd = flow({ device: 'dev-1', forwarded: true, src: '198.51.100.7', dst: '203.0.113.1', dport: 8080 });
    expect(explainRequest(fwd)).toEqual({ src: '198.51.100.7', dst: '203.0.113.1', protocol: 'tcp', port: 8080 });
    expect(explainRequest(flow({ dport: 22 }))).toEqual({ src: '10.10.0.42', dst: '203.0.113.10', protocol: 'tcp', port: 22 });
  });

  it('carries the request in the address of the Faults screen and reads it back', () => {
    const q = explainQuery(explainRequest(flow({ device: 'dev-1', dport: 8883 })));
    expect(q).toEqual({ explain: '1', device: 'dev-1', dst: '203.0.113.10', protocol: 'tcp', port: '8883' });
    expect(explainFromQuery(q)).toEqual({ source: 'device', device: 'dev-1', src: '', dst: '203.0.113.10', protocol: 'tcp', port: '8883' });
    const f = explainQuery({ src: '198.51.100.7', dst: '203.0.113.1', protocol: 'tcp', port: 8080 });
    expect(explainFromQuery(f)).toEqual({ source: 'address', device: '', src: '198.51.100.7', dst: '203.0.113.1', protocol: 'tcp', port: '8080' });
    expect(explainFromQuery({ ...f, protocol: 'sctp' })?.protocol).toBe('any');
  });

  it('reads no request from an address that does not ask for one', () => {
    expect(explainFromQuery({})).toBeUndefined();
    expect(explainFromQuery({ device: 'x', dst: '1.1.1.1' })).toBeUndefined();
    expect(explainFromQuery({ explain: '1', dst: '1.1.1.1' })).toBeUndefined();
    expect(explainFromQuery({ explain: '1', device: 'x' })).toBeUndefined();
  });
});

describe('the destinations of the path view', () => {
  it('lists the destinations of the connections, the one that moved the most first, and sums them', () => {
    const t = pathTargets([
      flow({ id: '1', dport: 8883, dst_hostname: 'broker.example.com', upload: { packets: 1, bytes: 100 }, download: { packets: 1, bytes: 50 } }),
      flow({ id: '2', dport: 8883, upload: { packets: 1, bytes: 10 } }),
      flow({ id: '3', dst: '192.0.2.5', dport: 443, upload: { packets: 1, bytes: 9000 } }),
      flow({ id: '4', protocol: 'udp', dst: '192.0.2.123', dport: 123 }),
    ]);
    expect(t.map((x) => x.label)).toEqual(['HTTPS 192.0.2.5:443', 'MQTT/TLS broker.example.com:8883', 'NTP 192.0.2.123:123']);
    expect(t[1]).toMatchObject({ dst: '203.0.113.10', hostname: 'broker.example.com', protocol: 'tcp', port: 8883, bytes: 160, connections: 2 });
  });

  it('leaves out a forwarded connection and a protocol that cannot be explained, and joins the pings of one host', () => {
    const t = pathTargets([
      flow({ forwarded: true, dport: 8080 }),
      flow({ protocol: 'other' }),
      flow({ id: 'p1', protocol: 'icmp', dst: '192.0.2.9' }),
      flow({ id: 'p2', protocol: 'icmp', dst: '192.0.2.9' }),
    ]);
    expect(t.map((x) => [x.label, x.connections])).toEqual([['ICMP 192.0.2.9', 2]]);
    expect(pathTargets([])).toEqual([]);
  });
});

const explanation = (o: Partial<Explanation>): Explanation => ({
  generation: 1,
  source: {},
  access: { verdict: 'allow', layer: 'access_matrix' },
  faults: [],
  ...o,
});

describe('the badge of the path', () => {
  it('has nothing for a path as configured', () => {
    expect(pathState(undefined)).toEqual({ faults: [], elsewhere: [] });
    expect(pathState(explanation({}))).toEqual({ faults: [], elsewhere: [] });
    expect(pathState(explanation({ faults: [{ family: 'impairment' }] }))).toEqual({ faults: [], elsewhere: [] });
  });

  it('lists the fault that wins in each family that acts on the connection by its summary, its name or its profile', () => {
    const e = explanation({
      faults: [
        { family: 'impairment', winner: { family: 'impairment', layer: 'overlay', id: 'a', summary: '↑↓ 200 ms ±50' } },
        { family: 'mtu', winner: { family: 'mtu', layer: 'overlay', id: 'c', profile: { name: 'lte' } } },
        { family: 'tls', winner: { family: 'tls', layer: 'overlay', id: 'd' } },
        { family: 'tunnel', winner: { family: 'tunnel', layer: 'overlay', id: 'f', summary: 'wireguard blackhole' } },
      ],
    });
    expect(pathState(e).faults).toEqual([
      { family: 'impairment', text: '↑↓ 200 ms ±50' },
      { family: 'mtu', text: 'profile LTE' },
      { family: 'tls', text: 'tls' },
      { family: 'tunnel', text: 'wireguard blackhole' },
    ]);
    expect(faultBadgeText({ family: 'dhcp', layer: 'config', id: 'e' })).toBe('dhcp');
  });

  it('keeps a DNS or DHCP fault off the path: GET /explain resolves them for every destination, and they do not act on a connection (P6-AUDIT-50)', () => {
    const e = explanation({
      faults: [
        { family: 'dns', winner: { family: 'dns', layer: 'config', id: 'b', name: 'slow-dns' } },
        { family: 'dhcp', winner: { family: 'dhcp', layer: 'overlay', id: 'g', summary: 'dhcp short_lease 30s' } },
      ],
    });
    expect(pathState(e)).toEqual({
      faults: [],
      elsewhere: [
        { family: 'dns', text: 'slow-dns' },
        { family: 'dhcp', text: 'dhcp short_lease 30s' },
      ],
    });
  });

  it('says when an access rule stops the connection', () => {
    expect(pathState(explanation({ access: { verdict: 'drop', layer: 'config_rule' } })).blocked).toBe('Dropped by a configured rule');
    expect(pathState(explanation({ access: { verdict: 'reject', layer: 'access_matrix' } })).blocked).toBe('Rejected by the access matrix');
  });

  it('calls a TCP reset a reset, not a drop (P6-AUDIT-53)', () => {
    expect(pathState(explanation({ access: { verdict: 'reset', layer: 'overlay_rule' } })).blocked).toBe('Reset by an overlay rule');
  });

  it('carries the address the connections came from, for the question to the gateway (P6-AUDIT-58)', () => {
    const t = pathTargets([
      flow({ id: '1', src: '10.10.0.50', dport: 8883, upload: { packets: 1, bytes: 100 } }),
      flow({ id: '2', src: '10.10.0.20', dport: 8883, upload: { packets: 1, bytes: 10 } }),
    ]);
    expect(t[0]!.src).toBe('10.10.0.50');
  });
});

describe('the recent DNS queries', () => {
  it('shows the addresses, else why there are none', () => {
    expect(dnsAnswerText({ rcode: 'NOERROR', answers: ['203.0.113.10', '203.0.113.11'] })).toBe('203.0.113.10, 203.0.113.11');
    expect(dnsAnswerText({ rcode: 'NOERROR' })).toBe('empty answer');
    expect(dnsAnswerText({ rcode: 'NXDOMAIN' })).toBe('NXDOMAIN');
    expect(dnsAnswerText({ rcode: 'TIMEOUT' })).toBe('no answer');
  });

  it('tells a changed answer, a failed one and an ordinary one apart', () => {
    expect(dnsQueryTone({ rcode: 'NOERROR' })).toBe('normal');
    expect(dnsQueryTone({ rcode: 'NXDOMAIN' })).toBe('block');
    expect(dnsQueryTone({ rcode: 'NXDOMAIN', fault: 'f' })).toBe('fault');
  });
});
