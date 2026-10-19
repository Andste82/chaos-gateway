// Faults and profiles around a test of the device, with the generated TypeScript client.
//
// A real suite would run its own check of the device where the comments say so (publish a message, ask for a
// firmware update, ...); the gateway side is what these tests show.
import { createOverlay, getDevice, getOverlay, listOverlays } from '../../typescript/src';
import type { Overlay } from '../../typescript/src/model';
import { dut, ok, sleep, withFault } from './helpers';

const overlayIds = async () => (ok(await listOverlays()) as { items: Overlay[] }).items.map((o) => o.id);

test('the device is online before the test degrades its network', async () => {
  const device = ok(await getDevice(dut));
  expect(device.observed.online).toBe(true);
});

test('a fault is on inside the block and gone after it', async () => {
  let id = '';
  await withFault({ latency: '200ms', jitter: '50ms', loss: '2%' }, async (overlay) => {
    id = overlay.id;
    // the call returned after the gateway verified the change: the fault is in the kernel now
    const current = ok(await getOverlay(overlay.id));
    expect(current.fault?.latency).toBe('200ms');
    expect(current.ttl).toBe('5m');
    expect(current.expires_at).toBeDefined();

    // ... run the test of the device under a slow, lossy link here ...
  });
  expect(await overlayIds()).not.toContain(id);
});

test('a fault is removed even when the test fails', async () => {
  let id = '';
  await expect(
    withFault({ blackout: true }, async (overlay) => {
      id = overlay.id;
      throw new Error('the device did not do what the test expected');
    }),
  ).rejects.toThrow('the device did not');
  expect(await overlayIds()).not.toContain(id);
});

test('a profile with a short TTL removes itself', async () => {
  const overlay = ok(await createOverlay({ target: { device: dut }, profile: 'bad-lte', ttl: '1s' })) as Overlay;
  const deadline = Date.now() + 10_000;
  while ((await overlayIds()).includes(overlay.id)) {
    if (Date.now() > deadline) throw new Error('the profile is still active after its TTL');
    await sleep(200);
  }
});
