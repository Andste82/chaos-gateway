import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import type { Configuration, PortForward, RevisionWithConfiguration } from '../api/generated/model';
import { useDraftStore } from './draft';

// The port forwards in the configuration draft (M35a): their changes in words, their merge patch, the
// gateway's messages about them, and the concurrent-change dialog's overlaps and reapply.

const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';
const WEB = 'e1000000-0000-4000-8000-000000000001';
const NEW = 'e1000000-0000-4000-8000-0000000000ff';
const names = { device: (id: string) => ({ [ESP]: 'ESP32-42' })[id] };

const web: PortForward = { name: 'esp-web', protocol: 'tcp', entry: 'uplink', external_port: 8080, target: { device: ESP }, target_port: 80 };

function config(forwards: Record<string, PortForward> = { [WEB]: web }): Configuration {
  return { schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, port_forwards: forwards };
}

function revision(id: number, configuration: Configuration = config()): RevisionWithConfiguration {
  return { id, status: 'active', schema_version: 1, created_at: '', created_by: { type: 'user', id: 'admin' }, configuration };
}

beforeEach(() => setActivePinia(createPinia()));

describe('the port forwards in the draft (M35a)', () => {
  it('a new forward is a change in words and the whole forward in the patch; taken back it is none', () => {
    const d = useDraftStore();
    d.track(revision(42));
    const added: PortForward = { protocol: 'udp', entry: 'uplink', external_port: 8000, external_port_end: 8010, target: { address: '10.20.0.50' }, target_port: 9000 };
    d.setForward(NEW, added);
    expect(d.changes(names)).toEqual([
      { kind: 'forward', id: NEW, name: 'UDP 8000-8010', field: 'forward', from: '—', to: 'added (UDP 8000-8010 → 10.20.0.50:9000-9010 from the uplink)' },
    ]);
    expect(d.summary(names)).toBe('1 change in 1 port forward');
    expect(d.patch()).toEqual({ port_forwards: { [NEW]: added } });
    expect(d.effectiveForwards[NEW]).toEqual(added);
    d.setForward(NEW, null);
    expect(d.dirty).toBe(false);
  });

  it('a forward disabled is the member that changed; deleted it is null; Undo brings it back', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setForward(WEB, { ...web, enabled: false });
    expect(d.changes(names)).toEqual([{ kind: 'forward', id: WEB, name: 'esp-web', field: 'enabled', from: 'yes', to: 'no' }]);
    expect(d.patch()).toEqual({ port_forwards: { [WEB]: { enabled: false } } });
    d.setForward(WEB, null);
    expect(d.patch()).toEqual({ port_forwards: { [WEB]: null } });
    expect(d.summary(names)).toBe('1 change in 1 port forward');
    d.restoreForward(WEB);
    expect(d.dirty).toBe(false);
  });

  it("the gateway's messages are kept by forward with the pointer below it, and an edit of the forward clears them", () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setForward(NEW, { protocol: 'tcp', entry: 'uplink', external_port: 443, target: { device: ESP } });
    d.setServerErrors([
      { path: `/port_forwards/${NEW}/external_port`, message: '443/tcp is the UI/API port' },
      { path: `/port_forwards/${WEB}`, message: 'not valid' },
    ]);
    expect(d.serverErrors[NEW]).toEqual({ external_port: '443/tcp is the UI/API port' });
    expect(d.serverErrors[WEB]).toEqual({ forward: 'not valid' });
    d.setForward(NEW, { protocol: 'tcp', entry: 'uplink', external_port: 8443, target: { device: ESP } });
    expect(d.serverErrors[NEW]).toBeUndefined();
  });

  it('a forward both changed is named, theirs → mine; reapplied, the fields only they changed stay theirs', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setForward(WEB, { ...web, external_port: 8081 });
    // they changed the port too, and the target port
    const theirs = config({ [WEB]: { ...web, external_port: 8082, target_port: 8000 } });
    expect(d.overlaps(theirs, names)).toEqual([{ kind: 'forward', id: WEB, name: 'esp-web', field: 'external port', from: '8082', to: '8081' }]);
    d.rebase(revision(43, theirs));
    expect(d.patch()).toEqual({ port_forwards: { [WEB]: { external_port: 8081 } } });
    expect(d.effectiveForwards[WEB]).toEqual({ ...web, external_port: 8081, target_port: 8000 });
  });

  it('a target both changed is one choice: a device of theirs against an address of mine is named in words, and mine wins whole', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setForward(WEB, { ...web, target: { address: '10.10.0.20' } });
    const theirs = config({ [WEB]: { ...web, target: { device: 'a1b2c3d4-0000-4000-8000-000000000057' } } });
    expect(d.overlaps(theirs, { device: (id) => ({ [ESP]: 'ESP32-42', 'a1b2c3d4-0000-4000-8000-000000000057': 'Sensor-07' })[id] })).toEqual([
      { kind: 'forward', id: WEB, name: 'esp-web', field: 'target', from: 'Sensor-07', to: '10.10.0.20' },
    ]);
    d.rebase(revision(43, theirs));
    expect(d.effectiveForwards[WEB]!.target).toEqual({ address: '10.10.0.20' });
  });

  it('a forward they deleted that I changed comes back as mine; one I deleted that they removed drops out', () => {
    const d = useDraftStore();
    d.track(revision(42));
    d.setForward(WEB, { ...web, enabled: false });
    expect(d.overlaps(config({}), names)).toEqual([{ kind: 'forward', id: WEB, name: 'esp-web', field: 'forward', from: 'deleted', to: 'kept with my change' }]);
    d.rebase(revision(43, config({})));
    expect(d.patch()).toEqual({ port_forwards: { [WEB]: { ...web, enabled: false } } });
    d.discard();
    d.track(revision(44));
    d.setForward(WEB, null);
    d.rebase(revision(45, config({})));
    expect(d.dirty).toBe(false);
  });
});
