import { describe, expect, it } from 'vitest';
import { CreateOverlayBody } from '../api/generated/zod/overlays/overlays';
import { dnsBody, dnsEffect, dnsErrors, dnsFieldOf, dnsScope, dnsToForm, emptyDns, type DnsForm } from './dnsForm';

// The DNS form (M23b): the fields of each action, the checks under them, the `dns` part of the request it becomes (checked
// against the spec) and the field a message of the gateway belongs to.

const form = (over: Partial<DnsForm>): DnsForm => ({ ...emptyDns(), ...over });
const spec = (dns: unknown) => CreateOverlayBody.safeParse({ target: { global: true }, dns }).success;

describe('the DNS form', () => {
  it('sends only the fields its action uses, whatever else the form holds', () => {
    expect(dnsBody(form({ action: 'nxdomain', names: 'broker.example.com' }))).toEqual({ action: 'nxdomain', names: ['broker.example.com'] });
    expect(dnsBody(form({ action: 'servfail' }))).toEqual({ action: 'servfail' });
    expect(dnsBody(form({ action: 'timeout', delay: '9s', ttl: '1s' }))).toEqual({ action: 'timeout' });
    expect(dnsBody(form({ action: 'delay', delay: ' 500ms ', names: '*.cdn.example.net, broker.example.com' }))).toEqual({
      action: 'delay',
      delay: '500ms',
      names: ['*.cdn.example.net', 'broker.example.com'],
    });
    expect(dnsBody(form({ action: 'wrong_answer', answers: '10.9.0.1  10.9.0.2' }))).toEqual({ action: 'wrong_answer', answers: ['10.9.0.1', '10.9.0.2'] });
    expect(dnsBody(form({ action: 'truncate' }))).toEqual({ action: 'truncate' });
    expect(dnsBody(form({ action: 'short_ttl', ttl: '0s' }))).toEqual({ action: 'short_ttl', ttl: '0s' });
  });

  it('every action the form makes is a DNS fault the spec accepts', () => {
    for (const f of [
      form({ action: 'nxdomain' }),
      form({ action: 'servfail', names: 'a.example.com' }),
      form({ action: 'timeout' }),
      form({ action: 'delay', delay: '2s' }),
      form({ action: 'wrong_answer', answers: '10.9.0.1' }),
      form({ action: 'truncate', names: '*.example.com' }),
      form({ action: 'short_ttl', ttl: '5s' }),
    ]) {
      expect(dnsErrors(f)).toEqual({});
      expect(spec(dnsBody(f))).toBe(true);
    }
  });

  it('checks the names, the delay, the addresses and the TTL', () => {
    expect(dnsErrors(form({ names: 'broker.example.com, *.example.net' }))).toEqual({});
    expect(dnsErrors(form({ names: 'broker.example.com, not a name!' })).dnsNames).toMatch(/is no host name/);
    expect(dnsErrors(form({ names: 'a.example.com A.EXAMPLE.COM' })).dnsNames).toBe('Each name once.');
    // the other fields are not checked while their action is not chosen
    expect(dnsErrors(form({ action: 'nxdomain', delay: 'soon', answers: 'x', ttl: '-1' }))).toEqual({});
    expect(dnsErrors(form({ action: 'delay', delay: '' })).dnsDelay).toBeDefined();
    expect(dnsErrors(form({ action: 'delay', delay: '0s' })).dnsDelay).toBeDefined();
    expect(dnsErrors(form({ action: 'delay', delay: '2' })).dnsDelay).toBeDefined();
    expect(dnsErrors(form({ action: 'delay', delay: '1m30s' }))).toEqual({});
    expect(dnsErrors(form({ action: 'wrong_answer', answers: '' })).dnsAnswers).toMatch(/Give the IPv4 address/);
    expect(dnsErrors(form({ action: 'wrong_answer', answers: '10.9.0.1, nope' })).dnsAnswers).toBe('nope is no IPv4 address.');
    expect(dnsErrors(form({ action: 'wrong_answer', answers: '0.0.0.0' })).dnsAnswers).toBeDefined();
    expect(dnsErrors(form({ action: 'short_ttl', ttl: '' })).dnsTtl).toBeDefined();
    // a TTL of zero is a legitimate test: an answer that must not be cached
    expect(dnsErrors(form({ action: 'short_ttl', ttl: '0s' }))).toEqual({});
  });

  it('reads a fault back into the form and sends it unchanged', () => {
    for (const d of [
      { action: 'nxdomain' as const },
      { action: 'delay' as const, delay: '1500ms', names: ['broker.example.com', '*.example.net'] },
      { action: 'wrong_answer' as const, answers: ['10.9.0.1'], names: ['broker.example.com'] },
      { action: 'short_ttl' as const, ttl: '0s' },
    ]) {
      expect(dnsBody(dnsToForm(d))).toEqual(d);
    }
  });

  it('says in words what the fault does', () => {
    expect(dnsScope({ action: 'nxdomain' })).toBe('DNS queries for every name');
    expect(dnsScope({ action: 'nxdomain', names: ['a.example.com', '*.b.example.com'] })).toBe('DNS queries for a.example.com, *.b.example.com');
    expect(dnsEffect({ action: 'nxdomain' })).toBe('NXDOMAIN (no such name)');
    expect(dnsEffect({ action: 'timeout' })).toBe('no answer');
    expect(dnsEffect({ action: 'delay', delay: '2s' })).toBe('answers delayed by 2s');
    expect(dnsEffect({ action: 'wrong_answer', answers: ['10.9.0.1', '10.9.0.2'] })).toBe('answers 10.9.0.1, 10.9.0.2');
    expect(dnsEffect({ action: 'short_ttl', ttl: '5s' })).toBe('TTL set to 5s');
  });

  it('puts a problem of the gateway under its field', () => {
    expect(dnsFieldOf('/names/0')).toBe('dnsNames');
    expect(dnsFieldOf('/delay')).toBe('dnsDelay');
    expect(dnsFieldOf('/answers/1')).toBe('dnsAnswers');
    expect(dnsFieldOf('/ttl')).toBe('dnsTtl');
    expect(dnsFieldOf('/action')).toBe('dnsAction');
    expect(dnsFieldOf('')).toBeUndefined();
    expect(dnsFieldOf(undefined)).toBeUndefined();
  });
});
