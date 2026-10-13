import { describe, expect, it } from 'vitest';
import { groupExcerpt, names, nftExcerpt } from './linuxView';

const nft = `table inet chaosgw {
  counter forward_drop { packets 3 bytes 180 }
  set ifs_lan_bf4feb {
    type ifname
    elements = { "br-iot", "br-test" }
  }
  set mgmt_src {
    type ipv4_addr
    flags interval
    elements = { 192.168.56.0/24 }
  }
  chain forward {
    type filter hook forward priority 0; policy drop;
    ct state { established, related } accept
    iifname "br-iot" oifname "wan0" counter packets 12 bytes 840 accept
    iifname "br-test" oifname "wan0" accept
    iifname "br-iot2" oifname "wan0" accept
  }
  chain input {
    type filter hook input priority 0; policy accept;
    iifname "br-test" accept
  }
}`;

describe('the Linux view of one object', () => {
  it('names a term as a whole word only', () => {
    expect(names('iifname "br-iot" accept', ['br-iot'])).toBe(true);
    expect(names('iifname "br-iot2" accept', ['br-iot'])).toBe(false);
    expect(names('route table 100 10.10.0.0/24 dev br-iot', ['10.10.0.0/24'])).toBe(true);
    expect(names('route table 100 110.10.0.0/24', ['10.10.0.0/24'])).toBe(false);
    expect(names('br-iot: qdisc 12: parent 1:12 netem', ['br-iot'])).toBe(true);
  });

  it('keeps the nftables lines of a network with the table and the chain or set around them', () => {
    expect(nftExcerpt(nft, ['br-iot'])).toBe(`table inet chaosgw {
  set ifs_lan_bf4feb {
    type ifname
    elements = { "br-iot", "br-test" }
  }
  chain forward {
    type filter hook forward priority 0; policy drop;
    iifname "br-iot" oifname "wan0" counter packets 12 bytes 840 accept
  }
}`);
    expect(nftExcerpt(nft, ['br-none'])).toBe('');
  });

  it('keeps a chain whole when asked for it (the forward chain of the access matrix)', () => {
    const forward = nftExcerpt(nft, [], (h) => h.startsWith('  chain forward {'));
    expect(forward.split('\n')).toEqual([
      'table inet chaosgw {',
      '  chain forward {',
      '    type filter hook forward priority 0; policy drop;',
      '    ct state { established, related } accept',
      '    iifname "br-iot" oifname "wan0" counter packets 12 bytes 840 accept',
      '    iifname "br-test" oifname "wan0" accept',
      '    iifname "br-iot2" oifname "wan0" accept',
      '  }',
      '}',
    ]);
  });

  it('keeps a group whose head names the term whole, else only the lines that name it', () => {
    const routes = `bridge br-iot up
  address 10.10.0.1/24
  port lan0
bridge br-test up
  address 10.20.0.1/24
rule 1000 iif br-iot lookup 100
route table 100 10.10.0.0/24 dev br-iot
route table 100 10.20.0.0/24 dev br-test`;
    expect(groupExcerpt(routes, ['br-iot', '10.10.0.0/24']).split('\n')).toEqual([
      'bridge br-iot up',
      '  address 10.10.0.1/24',
      '  port lan0',
      'rule 1000 iif br-iot lookup 100',
      'route table 100 10.10.0.0/24 dev br-iot',
    ]);
    const tc = `br-iot: qdisc 12: parent 1:12 netem limit=1000 delay=0.05
br-iot:   sent 840 bytes 12 pkt (dropped 0, overlimits 0, requeues 0) backlog 0b 0p
wan0: qdisc 12: parent 1:12 netem limit=1000 delay=0.05`;
    expect(groupExcerpt(tc, ['br-iot']).split('\n')).toHaveLength(2);
  });
});
