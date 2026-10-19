import { spawnSync, SpawnSyncReturns } from 'node:child_process';
import { deleteOverlay, createOverlay } from '../../typescript/src';
import type { Overlay, OverlayRequest } from '../../typescript/src/model';

/** The device under test. */
export const dut = process.env.CHAOSGW_DEVICE ?? 'esp32-42';

/** The body of a response, or a thrown error with the gateway's own explanation. */
export function ok<T extends { status: number; data: unknown }>(res: T): Extract<T, { status: 200 | 201 | 204 }>['data'] {
  if (res.status < 200 || res.status > 299) {
    const p = res.data as { code?: string; detail?: string; errors?: { path: string; message: string }[] };
    const where = (p.errors ?? []).map((e) => `${e.path}: ${e.message}`).join('; ');
    throw new Error(`${res.status} ${p.code}: ${p.detail ?? ''} ${where}`);
  }
  return res.data as never;
}

/**
 * Runs `body` with a fault on the device under test and removes the fault afterwards, whether the body succeeds or
 * not. Every overlay has a TTL, so a run that dies without its `finally` leaves nothing behind either.
 */
export async function withFault<T>(fault: NonNullable<OverlayRequest['fault']>, body: (overlay: Overlay) => Promise<T>): Promise<T> {
  const overlay = ok(await createOverlay({ target: { device: dut }, ttl: '5m', fault })) as Overlay;
  try {
    return await body(overlay);
  } finally {
    const res = await deleteOverlay(overlay.id);
    if (res.status !== 204 && res.status !== 404) ok(res); // 404: the TTL ran out first
  }
}

/**
 * Runs chaosctl (CHAOSCTL, default: the one on the PATH) with the environment of the suite. chaosctl is a Go program: it
 * ignores NODE_EXTRA_CA_CERTS, which Node uses for the fetch of the generated client, and trusts a gateway with a
 * self-signed certificate only through CHAOSGW_CA_CERT, CHAOSGW_FINGERPRINT or CHAOSGW_INSECURE. So the file of
 * NODE_EXTRA_CA_CERTS is handed on as CHAOSGW_CA_CERT unless the environment names a way of its own.
 */
export function chaosctl(...args: string[]): SpawnSyncReturns<string> {
  const env = { ...process.env };
  const own = env.CHAOSGW_CA_CERT || env.CHAOSGW_FINGERPRINT || env.CHAOSGW_INSECURE;
  if (!own && env.NODE_EXTRA_CA_CERTS) env.CHAOSGW_CA_CERT = env.NODE_EXTRA_CA_CERTS;
  return spawnSync(process.env.CHAOSCTL ?? 'chaosctl', args, { encoding: 'utf8', timeout: 120_000, env });
}

export const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
