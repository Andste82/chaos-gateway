import { describe, expect, it } from 'vitest';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import {
  dhcpActionLabels,
  dhcpActions,
  dhcpActionsFor,
  dhcpBody,
  dhcpEffect,
  dhcpErrors,
  dhcpFieldOf,
  dhcpScopeOf,
  dhcpToForm,
  emptyDhcp,
  type DhcpForm,
} from './dhcpForm';

// The DHCP form (M23b): the five actions, what each needs, the checks under the fields, the `dhcp` part of the request it
// becomes (checked against the spec), the actions a network has and a device has, and the field a message of the gateway
// belongs to.

const form = (over: Partial<DhcpForm>): DhcpForm => ({ ...emptyDhcp(), ...over });
const spec = (dhcp: unknown, target: unknown = { device: 'esp32-42' }) => CreateOverlayBody.safeParse({ target, dhcp }).success;

describe('the DHCP form', () => {
  it('has the five actions, and a network has no forced new IP', () => {
    expect(dhcpActions.map((a) => a.value)).toEqual(['short_lease', 'delete_lease', 'force_new_ip', 'set_options', 'silence']);
    expect(dhcpActionsFor('device').map((a) => a.value)).toEqual(['short_lease', 'delete_lease', 'force_new_ip', 'set_options', 'silence']);
    expect(dhcpActionsFor('network').map((a) => a.value)).toEqual(['short_lease', 'delete_lease', 'set_options', 'silence']);
    expect(dhcpActionLabels.force_new_ip).toBe('Force a new IP');
    expect(dhcpScopeOf({ device: 'd' })).toBe('device');
    expect(dhcpScopeOf({ network: 'n' })).toBe('network');
    expect(dhcpScopeOf({ group: 'g' })).toBeUndefined();
    expect(dhcpScopeOf({ global: true })).toBeUndefined();
    expect(dhcpScopeOf(undefined)).toBeUndefined();
  });

  it('sends only the fields its action uses', () => {
    expect(dhcpBody(form({ action: 'short_lease', leaseTime: ' 7s ', ip: '10.0.0.1', router: '10.0.0.2' }))).toEqual({ action: 'short_lease', lease_time: '7s' });
    expect(dhcpBody(form({ action: 'delete_lease', leaseTime: '7s' }))).toEqual({ action: 'delete_lease' });
    expect(dhcpBody(form({ action: 'silence' }))).toEqual({ action: 'silence' });
    // the gateway chooses the address when the form names none
    expect(dhcpBody(form({ action: 'force_new_ip' }))).toEqual({ action: 'force_new_ip' });
    expect(dhcpBody(form({ action: 'force_new_ip', ip: ' 10.10.0.77 ' }))).toEqual({ action: 'force_new_ip', ip: '10.10.0.77' });
  });

  it('set options sends the fields that are set, as the action replaces them', () => {
    const f = form({ action: 'set_options', router: '10.10.0.9', dnsServers: '192.0.2.53, 192.0.2.54', domain: 'lab.example', ntp: '192.0.2.123', custom: '66 tftp.lab\n67 boot.img' });
    expect(dhcpErrors(f)).toEqual({});
    expect(dhcpBody(f)).toEqual({
      action: 'set_options',
      options: { router: '10.10.0.9', dns_servers: ['192.0.2.53', '192.0.2.54'], domain: 'lab.example', ntp_servers: ['192.0.2.123'], custom: [{ code: 66, value: 'tftp.lab' }, { code: 67, value: 'boot.img' }] },
    });
    expect(dhcpBody(form({ action: 'set_options', router: '10.10.0.9' }))).toEqual({ action: 'set_options', options: { router: '10.10.0.9' } });
    // an empty domain takes the domain away
    expect(dhcpBody(form({ action: 'set_options', removeDomain: true, domain: 'ignored.example' }))).toEqual({ action: 'set_options', options: { domain: '' } });
  });

  it('every action the form makes is an action the spec accepts', () => {
    for (const f of [
      form({ action: 'short_lease', leaseTime: '7s' }),
      form({ action: 'delete_lease' }),
      form({ action: 'force_new_ip' }),
      form({ action: 'force_new_ip', ip: '10.10.0.77' }),
      form({ action: 'silence' }),
      form({ action: 'set_options', dnsServers: '192.0.2.53' }),
      form({ action: 'set_options', removeDomain: true }),
      form({ action: 'set_options', custom: '66 tftp.lab' }),
    ]) {
      expect(dhcpErrors(f)).toEqual({});
      expect(spec(dhcpBody(f))).toBe(true);
    }
    // an action of a network too
    expect(spec(dhcpBody(form({ action: 'silence' })), { network: 'IoT' })).toBe(true);
  });

  it('checks the lease time, the address and the options', () => {
    expect(dhcpErrors(form({ action: 'short_lease', leaseTime: '' })).leaseTime).toBeDefined();
    expect(dhcpErrors(form({ action: 'short_lease', leaseTime: '500ms' })).leaseTime).toMatch(/at least 1s/);
    expect(dhcpErrors(form({ action: 'short_lease', leaseTime: '30' })).leaseTime).toBeDefined();
    expect(dhcpErrors(form({ action: 'short_lease', leaseTime: '1s' }))).toEqual({});
    expect(dhcpErrors(form({ action: 'force_new_ip', ip: 'nope' })).ip).toBeDefined();
    expect(dhcpErrors(form({ action: 'force_new_ip', ip: '224.0.0.1' })).ip).toBeDefined();
    expect(dhcpErrors(form({ action: 'force_new_ip', ip: '127.0.0.1' })).ip).toBeDefined();
    expect(dhcpErrors(form({ action: 'force_new_ip', ip: '' }))).toEqual({});
    const o = (over: Partial<DhcpForm>) => dhcpErrors(form({ action: 'set_options', router: '10.10.0.9', ...over }));
    expect(o({ router: 'gateway' }).router).toBeDefined();
    expect(o({ dnsServers: '192.0.2.53, x' }).dhcpDns).toBeDefined();
    expect(o({ ntp: '999.0.0.1' }).ntp).toBeDefined();
    expect(o({ domain: 'not a domain' }).domain).toBeDefined();
    expect(o({ removeDomain: true, domain: 'not a domain' })).toEqual({});
    expect(o({ custom: 'sixty-six' }).custom).toMatch(/one option per line/);
    expect(o({ custom: '0 x' }).custom).toMatch(/from 1 to 254/);
    expect(o({ custom: '255 x' }).custom).toMatch(/from 1 to 254/);
    expect(o({ custom: '15 lab.example' }).custom).toMatch(/set by the gateway/);
    expect(o({ custom: '66 a\n66 b' }).custom).toBe('Each option code once.');
    // an action that sets nothing is none
    expect(dhcpErrors(form({ action: 'set_options' })).options).toMatch(/at least one option/);
  });

  it('reads an action back into the form and sends it unchanged', () => {
    for (const d of [
      { action: 'short_lease' as const, lease_time: '7s' },
      { action: 'delete_lease' as const },
      { action: 'force_new_ip' as const, ip: '10.10.0.254' },
      { action: 'silence' as const },
      { action: 'set_options' as const, options: { router: '10.10.0.9', dns_servers: ['192.0.2.53'], domain: '', ntp_servers: ['192.0.2.123'], custom: [{ code: 66, value: 'tftp.lab' }] } },
    ]) {
      expect(dhcpBody(dhcpToForm(d))).toEqual(d);
    }
  });

  it('says in words what the action does, the address a device is moved to included', () => {
    expect(dhcpEffect({ action: 'short_lease', lease_time: '7s' })).toBe('leases of 7s');
    expect(dhcpEffect({ action: 'delete_lease' })).toBe('the leases are deleted');
    expect(dhcpEffect({ action: 'force_new_ip', ip: '10.10.0.254' })).toBe('moved to 10.10.0.254');
    expect(dhcpEffect({ action: 'force_new_ip' })).toBe('moved to an address the gateway chooses');
    expect(dhcpEffect({ action: 'silence' })).toBe('the server stays silent');
    expect(dhcpEffect({ action: 'set_options', options: { router: '10.10.0.9', domain: '', custom: [{ code: 66, value: 'x' }] } })).toBe('options: router 10.10.0.9, no domain, option 66');
  });

  it('puts a problem of the gateway under its field', () => {
    expect(dhcpFieldOf('/lease_time')).toBe('leaseTime');
    expect(dhcpFieldOf('/ip')).toBe('ip');
    expect(dhcpFieldOf('/options')).toBe('options');
    expect(dhcpFieldOf('/options/router')).toBe('router');
    expect(dhcpFieldOf('/options/dns_servers/0')).toBe('dhcpDns');
    expect(dhcpFieldOf('/options/ntp_servers/0')).toBe('ntp');
    expect(dhcpFieldOf('/options/custom/0/code')).toBe('custom');
    expect(dhcpFieldOf('/options/domain')).toBe('domain');
    expect(dhcpFieldOf('')).toBeUndefined();
  });
});
