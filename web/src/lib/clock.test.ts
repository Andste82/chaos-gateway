import { afterEach, describe, expect, it, vi } from 'vitest';
import { customFetch } from '../api/fetcher';
import { gatewayNow, gatewayOffset, noteGatewayDate, resetGatewayClock } from './clock';

afterEach(() => {
  resetGatewayClock();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const at = (iso: string) => Date.parse(iso);

describe('the gateway clock', () => {
  it('is the browser clock before the gateway answered', () => {
    expect(gatewayOffset()).toBe(0);
    noteGatewayDate(null, 0, 0);
    noteGatewayDate('not a date', 0, 0);
    expect(gatewayOffset()).toBe(0);
  });

  it('takes the offset from the Date header, within the second the header resolves', () => {
    // the browser is 90 s ahead of the gateway (a Raspberry Pi before NTP)
    const local = at('2026-10-13T12:01:30.200Z');
    noteGatewayDate('Tue, 13 Oct 2026 12:00:00 GMT', local - 40, local);
    expect(Math.abs(gatewayOffset() - -90_000)).toBeLessThanOrEqual(1000);
  });

  it('narrows the estimate with every answer', () => {
    // the gateway is exactly 30.6 s ahead; its header shows whole seconds
    const offset = 30_600;
    for (const local of [1_000_000, 1_000_250, 1_000_500, 1_000_800, 1_001_100]) {
      const header = new Date(Math.floor((local + offset) / 1000) * 1000).toUTCString();
      noteGatewayDate(header, local - 10, local);
    }
    expect(Math.abs(gatewayOffset() - offset)).toBeLessThanOrEqual(150);
  });

  it('starts over when the gateway clock was set meanwhile', () => {
    noteGatewayDate('Thu, 01 Jan 1970 00:16:40 GMT', 999_990, 1_000_000); // gateway at 1 000 s, browser too
    expect(Math.abs(gatewayOffset())).toBeLessThanOrEqual(1000);
    // NTP moved the gateway an hour ahead
    noteGatewayDate(new Date(1_010_000 + 3_600_000).toUTCString(), 1_009_990, 1_010_000);
    expect(Math.abs(gatewayOffset() - 3_600_000)).toBeLessThanOrEqual(1000);
  });

  it('learns from every answer customFetch receives', async () => {
    vi.useFakeTimers({ now: at('2026-10-13T12:02:00.500Z'), toFake: ['Date'] });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('{}', { status: 200, headers: { Date: 'Tue, 13 Oct 2026 12:00:00 GMT' } })),
    );
    await customFetch('/state', { method: 'GET' });
    const skew = gatewayNow().getTime() - at('2026-10-13T12:00:00.500Z');
    expect(Math.abs(skew)).toBeLessThanOrEqual(500);
  });
});
