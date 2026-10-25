import { describe, expect, it } from 'vitest';
import type { Capture, CaptureStorage } from '../api/generated/model';
import {
  captureErrors,
  captureFieldOf,
  captureState,
  describeCaptureScope,
  downloadUrl,
  emptyCaptureForm,
  formatSize,
  formToCaptureRequest,
  isDeviceCapture,
  parseSize,
  storageView,
} from './captures';

const base = { id: 'c1', started_at: '2026-10-25T10:00:00Z', scope: { uplink: true as const } };
const cap = (over: Partial<Capture>): Capture => ({ ...base, status: 'stopped', ...over });

describe('the state of a capture', () => {
  it('says running while it records', () => {
    expect(captureState(cap({ status: 'running' }))).toMatchObject({ label: 'Running', early: false });
  });

  it('tells the quota from the size limit the user chose: only the quota comes with a message', () => {
    const own = captureState(cap({ stop_reason: 'size' }));
    expect(own).toMatchObject({ label: 'Ended · size limit', early: false });
    const quota = captureState(cap({ stop_reason: 'size', message: 'the captures take 5.1 GiB, more than their quota of 5 GiB' }));
    expect(quota).toMatchObject({ label: 'Stopped · quota', tone: 'block', early: true });
    expect(quota.hint).toContain('quota of 5 GiB');
  });

  it('shows the free-space limit as such, with the message of the gateway', () => {
    const s = captureState(cap({ stop_reason: 'disk_low', message: 'the volume of the captures has less than 10 % free space' }));
    expect(s).toMatchObject({ label: 'Stopped · disk low', tone: 'block', early: true });
    expect(s.hint).toContain('10 % free space');
  });

  it('is a failure for a failed capture and one that stopped with an error', () => {
    expect(captureState(cap({ status: 'failed', stop_reason: 'error', message: 'the service restarted while the capture ran' }))).toMatchObject({ label: 'Failed', early: true });
    expect(captureState(cap({ stop_reason: 'error' }))).toMatchObject({ label: 'Failed', tone: 'block' });
  });

  it('is no early end when a user, the duration or the run ended it', () => {
    for (const stop_reason of ['user', 'duration', 'run_end'] as const) expect(captureState(cap({ stop_reason })).early).toBe(false);
    expect(captureState(cap({ stop_reason: 'run_end' })).label).toBe('Ended with its run');
  });
});

describe('sizes', () => {
  it('writes the binary units of the API', () => {
    expect(formatSize(0)).toBe('0 B');
    expect(formatSize(130 * 1024)).toBe('130 KiB');
    expect(formatSize(5 * 1024 ** 3)).toBe('5.0 GiB');
    expect(formatSize(1536)).toBe('1.5 KiB');
    expect(formatSize(undefined)).toBe('—');
  });

  it('reads a size as the gateway does', () => {
    expect(parseSize('64KiB')).toBe(65536);
    expect(parseSize(' 2 GiB ')).toBe(2 * 1024 ** 3);
    expect(parseSize('1.5GiB')).toBeUndefined();
    expect(parseSize('5GB')).toBeUndefined();
    expect(parseSize('')).toBeUndefined();
  });
});

describe('the storage', () => {
  const s: CaptureStorage = { quota_bytes: 5 * 1024 ** 3, used_bytes: 1024 ** 3, volume_total_bytes: 100 * 1024 ** 3, volume_free_bytes: 40 * 1024 ** 3, free_percent: 40, min_free_percent: 10, disk_low: false, running: 2, max_running: 8 };

  it('puts the captures against the quota and the volume against the limit', () => {
    expect(storageView(s)).toMatchObject({ usedPercent: 20, usedText: '1.0 GiB', quotaText: '5.0 GiB', freeText: '40 % free (40.0 GiB)', limitText: '10 %', low: false, full: false, runningText: '2 of 8 running' });
  });

  it('marks a volume below the limit and a quota that is used up', () => {
    const v = storageView({ ...s, used_bytes: s.quota_bytes + 5, free_percent: 4.2, disk_low: true });
    expect(v).toMatchObject({ usedPercent: 100, full: true, low: true });
    expect(v.freeText).toContain('4.2 %');
  });

  it('does not make up a free space the gateway could not read', () => {
    const { free_percent: _p, volume_free_bytes: _f, volume_total_bytes: _t, ...unread } = s;
    expect(storageView(unread).freeText).toBeUndefined();
  });
});

describe('the scope in words', () => {
  const names = { device: (id: string) => ({ d1: 'esp32-42' })[id], network: (id: string) => ({ n1: 'IoT' })[id], group: (id: string) => ({ g1: 'sensors' })[id] };

  it('names the device, the network, the uplink and a selector', () => {
    expect(describeCaptureScope({ device: 'd1' }, names)).toBe('Device esp32-42');
    expect(describeCaptureScope({ device: 'by-name' }, names)).toBe('Device by-name');
    expect(describeCaptureScope({ network: 'n1' }, names)).toBe('Network IoT');
    expect(describeCaptureScope({ uplink: true }, names)).toBe('Uplink');
    expect(describeCaptureScope({ selector: { source: { group: 'g1' }, protocol: 'tcp', ports: [8883], destination: { uplink: true } } }, names)).toBe('Selector Group sensors → uplink TCP 8883');
    expect(describeCaptureScope({ selector: { source: { global: true } } }, names)).toBe('Selector Everything');
  });

  it('knows the captures of a device by its id or its name, also as the source of a selector', () => {
    const d = { id: 'd1', name: 'ESP32-42' };
    expect(isDeviceCapture({ scope: { device: 'd1' } }, d)).toBe(true);
    expect(isDeviceCapture({ scope: { device: 'esp32-42' } }, d)).toBe(true);
    expect(isDeviceCapture({ scope: { selector: { source: { device: 'd1' }, protocol: 'tcp' } } }, d)).toBe(true);
    expect(isDeviceCapture({ scope: { selector: { source: { group: 'd1' } } } }, d)).toBe(false);
    expect(isDeviceCapture({ scope: { device: 'd2' } }, d)).toBe(false);
    expect(isDeviceCapture({ scope: { network: 'd1' } }, d)).toBe(false);
    expect(isDeviceCapture({ scope: { uplink: true } }, d)).toBe(false);
  });

  it('has a file and a key log to download', () => {
    expect(downloadUrl('c1')).toBe('/api/v1/captures/c1/download');
    expect(downloadUrl('c1', 'keylog')).toBe('/api/v1/captures/c1/download?part=keylog');
  });
});

describe('the form', () => {
  it('sends only what was set: a device with the defaults of the gateway', () => {
    expect(formToCaptureRequest(emptyCaptureForm('device', 'd1'))).toEqual({ scope: { device: 'd1' }, format: 'pcapng' });
  });

  it('makes a network, the uplink and a selector with its traffic', () => {
    const net = emptyCaptureForm('network');
    net.network = 'n1';
    expect(formToCaptureRequest(net).scope).toEqual({ network: 'n1' });
    expect(formToCaptureRequest(emptyCaptureForm('uplink')).scope).toEqual({ uplink: true });
    const sel = emptyCaptureForm('selector');
    sel.source = 'group:g1';
    sel.traffic = { protocol: 'tcp', ports: '8883, 1000-2000', destKind: 'cidr', destination: '203.0.113.0/24' };
    expect(formToCaptureRequest(sel).scope).toEqual({
      selector: { source: { group: 'g1' }, protocol: 'tcp', ports: [8883], port_ranges: [{ from: 1000, to: 2000 }], destination: { cidr: '203.0.113.0/24' } },
    });
    sel.source = 'global';
    sel.traffic = { protocol: 'any', ports: '', destKind: 'any', destination: '' };
    expect(formToCaptureRequest(sel).scope).toEqual({ selector: { source: { global: true } } });
  });

  it('sends the format, the packet length, the duration and the size limit', () => {
    const f = emptyCaptureForm('device', 'd1');
    f.name = ' bug 12 ';
    f.format = 'pcap';
    f.snaplen = '96';
    f.duration = '5m';
    f.maxSize = '512 MiB';
    expect(formToCaptureRequest(f)).toEqual({ name: 'bug 12', scope: { device: 'd1' }, format: 'pcap', snaplen: 96, duration: '5m', max_size: '512MiB' });
  });

  it('sends a ring buffer instead of a size limit, never both', () => {
    const f = emptyCaptureForm('uplink');
    f.maxSize = '1GiB';
    f.limit = 'ring';
    f.ringFiles = '10';
    f.ringFileSize = '16MiB';
    const r = formToCaptureRequest(f);
    expect(r.ring_buffer).toEqual({ files: 10, file_size: '16MiB' });
    expect(r.max_size).toBeUndefined();
  });

  it('has no problem in the defaults and names each field that is wrong', () => {
    expect(captureErrors(emptyCaptureForm('uplink'))).toEqual({});
    expect(captureErrors(emptyCaptureForm('device'))).toEqual({ device: 'Choose a device.' });
    expect(captureErrors(emptyCaptureForm('network'))).toEqual({ network: 'Choose a network.' });
    const f = emptyCaptureForm('uplink');
    f.name = 'x'.repeat(65);
    f.snaplen = '10';
    f.duration = 'soon';
    f.maxSize = '10KiB';
    expect(Object.keys(captureErrors(f)).sort()).toEqual(['duration', 'maxSize', 'name', 'snaplen']);
    f.snaplen = '262145';
    expect(captureErrors(f).snaplen).toBeDefined();
    f.snaplen = '262144';
    f.duration = '0s';
    expect(captureErrors(f).duration).toBeDefined();
    f.duration = '1h30m';
    f.maxSize = '12 gigs';
    expect(captureErrors(f).maxSize).toBe('A size like 512MiB or 2GiB.');
  });

  it('checks the ring buffer, not the size limit it replaces', () => {
    const f = emptyCaptureForm('uplink');
    f.maxSize = 'nonsense';
    f.limit = 'ring';
    f.ringFiles = '1';
    f.ringFileSize = '1KiB';
    expect(captureErrors(f)).toEqual({ ringFiles: 'From 2 to 100 files.', ringFileSize: 'At least 64KiB.' });
    f.ringFiles = '101';
    expect(captureErrors(f).ringFiles).toBeDefined();
    f.ringFiles = '100';
    f.ringFileSize = '64MiB';
    expect(captureErrors(f)).toEqual({});
  });

  it('checks the traffic of a selector, and its ports need TCP or UDP', () => {
    const f = emptyCaptureForm('selector');
    f.traffic = { protocol: 'icmp', ports: '80', destKind: 'any', destination: '' };
    expect(captureErrors(f).ports).toBeDefined();
    f.traffic = { protocol: 'tcp', ports: '80', destKind: 'hostname', destination: 'not a name' };
    expect(captureErrors(f).destination).toBeDefined();
    f.traffic = { protocol: 'tcp', ports: '80', destKind: 'cidr', destination: '203.0.113.0/24' };
    expect(captureErrors(f)).toEqual({});
  });

  it('finds the field a problem of the gateway names', () => {
    expect(captureFieldOf('/scope/device')).toBe('device');
    expect(captureFieldOf('/scope/network')).toBe('network');
    expect(captureFieldOf('/scope/selector/source')).toBe('source');
    expect(captureFieldOf('/scope/selector/destination/hostname')).toBe('destination');
    expect(captureFieldOf('/scope/selector/ports/0')).toBe('ports');
    expect(captureFieldOf('/max_size')).toBe('maxSize');
    expect(captureFieldOf('/ring_buffer/files')).toBe('ringFiles');
    expect(captureFieldOf('/ring_buffer')).toBe('ringFileSize');
    expect(captureFieldOf('/format')).toBe('format');
    expect(captureFieldOf('/duration')).toBe('duration');
    expect(captureFieldOf('/scope')).toBeUndefined();
    expect(captureFieldOf(undefined)).toBeUndefined();
  });
});
