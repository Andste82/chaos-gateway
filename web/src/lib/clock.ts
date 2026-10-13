// The gateway's clock as the browser sees it. Deadlines, TTLs and leases come from the gateway, so
// their countdowns must run on its clock, not on the browser's: a Raspberry Pi has no real-time
// clock and may be minutes off until NTP syncs, and a browser clock a minute ahead would end a
// 60 s commit-confirm countdown before it began.
//
// Every answer of the gateway carries its time in the Date header (whole seconds). The gateway's
// time when the answer arrived is at least that, and when the request left it was below that plus
// one second; each answer so bounds the offset between the two clocks. The bounds of all answers
// are intersected, which narrows the estimate below the second the header resolves; an answer that
// does not fit them (a clock that was set meanwhile) starts over from that answer.

let lo: number | undefined;
let hi: number | undefined;

/** Takes the Date header of an answer asked for at `sentAt` that arrived at `receivedAt` (local ms). */
export function noteGatewayDate(header: string | null | undefined, sentAt: number, receivedAt: number): void {
  if (!header) return;
  const at = Date.parse(header);
  if (Number.isNaN(at)) return;
  const sLo = at - receivedAt;
  const sHi = at + 1000 - sentAt;
  if (lo === undefined || hi === undefined || sLo > hi || sHi < lo) {
    lo = sLo;
    hi = sHi;
    return;
  }
  lo = Math.max(lo, sLo);
  hi = Math.min(hi, sHi);
}

/** How far the gateway's clock is ahead of the browser's, in ms (0 before the first answer). */
export function gatewayOffset(): number {
  return lo === undefined || hi === undefined ? 0 : Math.round((lo + hi) / 2);
}

/** The gateway's time now, as far as the browser can tell. */
export function gatewayNow(): Date {
  return new Date(Date.now() + gatewayOffset());
}

/** Forgets what was learned (tests). */
export function resetGatewayClock(): void {
  lo = undefined;
  hi = undefined;
}
