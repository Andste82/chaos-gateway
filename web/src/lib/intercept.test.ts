import { describe, expect, it } from 'vitest';
import type { InterceptFlow } from '../api/generated/model';
import { durationText, filtered, flowKeys, flowParams, hostFilterError, maxShown, noFilters, outcomeText, requestText, sizeText } from './intercept';

// The exchange table's filters and the words of a row (M23c).

const flow = (over: Partial<InterceptFlow>): InterceptFlow => ({ time: '2026-10-25T12:00:00Z', client: '10.10.0.42', original_dst: '203.0.113.10:443', protocol: 'http', ...over });

describe('the filters of the exchange table', () => {
  it('make the parameters of the request, with only what is set', () => {
    expect(flowParams(noFilters(), 100)).toEqual({ limit: 100 });
    expect(flowParams({ device: 'd1', host: ' *.example.com ', faulted: true }, 200)).toEqual({ limit: 200, device: 'd1', host: '*.example.com', faulted: true });
    // the API's limit per request is the most there is
    expect(flowParams(noFilters(), 5000).limit).toBe(maxShown);
  });

  it('do not send a host that is no name, and say so', () => {
    expect(hostFilterError('')).toBeUndefined();
    expect(hostFilterError('api.example.com')).toBeUndefined();
    expect(hostFilterError('*.example.com')).toBeUndefined();
    expect(hostFilterError('not a name!')).toMatch(/is no host name/);
    expect(flowParams({ device: '', host: 'not a name!', faulted: false }, 100)).toEqual({ limit: 100 });
  });

  it('know whether they are set', () => {
    expect(filtered(noFilters())).toBe(false);
    expect(filtered({ ...noFilters(), host: '  ' })).toBe(false);
    expect(filtered({ ...noFilters(), device: 'd1' })).toBe(true);
    expect(filtered({ ...noFilters(), host: 'a.example.com' })).toBe(true);
    expect(filtered({ ...noFilters(), faulted: true })).toBe(true);
  });
});

describe('an exchange in words', () => {
  it('says what it asked for', () => {
    expect(requestText(flow({ method: 'GET', host: 'api.example.com', path: '/v1/a' }))).toBe('GET api.example.com/v1/a');
    expect(requestText(flow({ method: 'GET', path: '/v1/a' }))).toBe('GET 203.0.113.10:443/v1/a');
    expect(requestText(flow({ protocol: 'tcp', host: 'broker.example.com' }))).toBe('broker.example.com');
    expect(requestText(flow({ protocol: 'tcp' }))).toBe('203.0.113.10:443');
    expect(requestText(flow({ method: 'GET', host: 'a.example.com', path: '' }))).toBe('GET a.example.com');
  });

  it('says how it ended', () => {
    expect(outcomeText(flow({ status: 503 }))).toBe('503');
    expect(outcomeText(flow({ protocol: 'tcp' }))).toBe('no HTTP');
    expect(outcomeText(flow({ error: 'the server did not answer' }))).toBe('no answer');
    expect(outcomeText(flow({}))).toBe('—');
  });

  it('says how big it was and how long it took', () => {
    expect(sizeText(flow({ request_bytes: 120, response_bytes: 1200 }))).toBe('120 B ↑ · 1.2 KB ↓');
    expect(sizeText(flow({}))).toBe('0 B ↑ · 0 B ↓');
    expect(sizeText(flow({ protocol: 'websocket', messages: 7 }))).toBe('0 B ↑ · 0 B ↓ · 7 messages');
    expect(durationText(flow({ duration_ms: 3.5 }))).toBe('3.5 ms');
    expect(durationText(flow({ duration_ms: 42.4 }))).toBe('42 ms');
    expect(durationText(flow({ duration_ms: 2000 }))).toBe('2.0 s');
    expect(durationText(flow({}))).toBe('—');
  });

  it('has a key for each row, the same exchanges apart', () => {
    const a = flow({ method: 'GET', host: 'a.example.com', path: '/x' });
    const keys = flowKeys([a, a, flow({ method: 'GET', host: 'a.example.com', path: '/y' })]);
    expect(new Set(keys).size).toBe(3);
    expect(flowKeys([a, a])[0]).toBe(flowKeys([a])[0]);
  });
});
