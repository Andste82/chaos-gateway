import { describe, expect, it } from 'vitest';
import { CreateRevisionBody } from '../api/generated/zod/revisions/revisions';
import { emptyProfileForm, formToProfile, profileFieldOf, profileFormErrors, profileToForm, type ProfileForm } from './profileForm';

const ID = 'c0000000-0000-4000-8000-0000000000c1';
/** The profile as the spec checks it in a revision. */
const valid = (p: unknown) => CreateRevisionBody.safeParse({ schema_version: 1, uplink: { interface: { name: 'eth0' } }, management: { interface: { name: 'eth1' } }, profiles: { [ID]: p } }).success;

function form(over: Partial<ProfileForm> = {}): ProfileForm {
  return { ...emptyProfileForm(), name: 'flaky', ...over };
}

describe('the profile editor (P3-M13-03)', () => {
  it('turns the form into a profile with an impairment and an MTU part, checked against the spec', () => {
    const f = form({ description: ' bursts ', mtuOn: true, mtuSize: '1400', mtuMode: 'mss_clamp' });
    f.impairment.latency = '80';
    f.impairment.lossModel = 'burst';
    f.impairment.burstP = '2';
    f.impairment.burstR = '40';
    const p = formToProfile(f);
    expect(p).toEqual({
      name: 'flaky',
      description: 'bursts',
      parts: { impairment: { latency: '80ms', burst_loss: { p: '2%', r: '40%' } }, mtu: { size: 1400, mode: 'mss_clamp' } },
    });
    expect(valid(p)).toBe(true);
    expect(profileFormErrors(f, [])).toEqual({});
    // read back and written again unchanged
    expect(formToProfile(profileToForm(p))).toEqual(p);
  });

  it('each direction its own parameters, and a part switched off', () => {
    const p = { name: 'asym', parts: { impairment: { upload: { latency: '10ms' }, download: { rate: '1Mbit' } } } };
    const f = profileToForm(p);
    expect(f.impairment.direction).toBe('each');
    expect(f.mtuOn).toBe(false);
    expect(formToProfile(f)).toEqual(p);
    expect(formToProfile({ ...f, impairmentOn: false, mtuOn: true })).toEqual({ name: 'asym', parts: { mtu: { size: 1280, mode: 'icmp' } } });
  });

  it("an MTU part without a mode keeps leaving out the default mode; a chosen one is written", () => {
    const base = { name: 'slow-lab', parts: { impairment: { latency: '20ms' }, mtu: { size: 1280 } } };
    const f = profileToForm(base);
    expect(f.mtuMode).toBe('icmp');
    expect(formToProfile(f, base)).toEqual(base);
    expect(formToProfile({ ...f, mtuSize: '1400' }, base).parts.mtu).toEqual({ size: 1400 });
    expect(formToProfile({ ...f, mtuMode: 'blackhole' }, base).parts.mtu).toEqual({ size: 1280, mode: 'blackhole' });
    // a base with the mode written keeps it written
    expect(formToProfile(f, { ...base, parts: { mtu: { size: 1280, mode: 'icmp' } } }).parts.mtu).toEqual({ size: 1280, mode: 'icmp' });
  });

  it('keeps a DNS or TLS part the editor does not make', () => {
    const base = { name: 'dns-and-delay', parts: { dns: { action: 'servfail' as const }, impairment: { latency: '5ms' } } };
    const f = profileToForm(base);
    expect(formToProfile({ ...f, impairmentOn: false }, base)).toEqual({ name: 'dns-and-delay', parts: { dns: { action: 'servfail' } } });
    // the kept part is a part: the profile is not empty without the others
    expect(profileFormErrors({ ...f, impairmentOn: false }, [], base).parts).toBeUndefined();
  });

  it('checks the name, the parts and the fields', () => {
    expect(profileFormErrors(form({ name: '' }), []).name).toBe('Give the profile a name.');
    expect(profileFormErrors(form({ name: '-x' }), []).name).toMatch(/^Letters, digits/);
    expect(profileFormErrors(form({ name: 'c0000000-0000-4000-8000-0000000000c1' }), []).name).toMatch(/^Letters, digits/);
    expect(profileFormErrors(form({ name: 'Bad-LTE' }), []).name).toBe('This is the name of a built-in profile.');
    expect(profileFormErrors(form({ name: 'Slow-Lab' }), ['slow-lab']).name).toBe('Another profile has this name.');
    expect(profileFormErrors(form({ impairmentOn: false }), []).parts).toBe('Give the profile an impairment or an MTU part.');
    // the impairment part is checked as in the fault dialog
    expect(profileFormErrors(form(), []).latency).toBe('Set at least one parameter: latency, loss, a rate, …');
    const f = form();
    f.impairment.latency = '10';
    f.impairment.jitter = '20';
    expect(profileFormErrors(f, []).jitter).toBe('Jitter cannot be larger than the latency.');
    expect(profileFormErrors(form({ impairmentOn: false, mtuOn: true, mtuSize: '9000' }), []).mtuSize).toBe('A packet size from 552 to 1500 bytes.');
    expect(profileFormErrors(form({ description: 'x'.repeat(501) }), []).description).toBe('At most 500 characters.');
  });

  it("the gateway's pointers below a profile name the editor's fields", () => {
    expect(profileFieldOf('name')).toBe('name');
    expect(profileFieldOf('parts')).toBe('parts');
    expect(profileFieldOf('parts/mtu/size')).toBe('mtuSize');
    expect(profileFieldOf('parts/mtu/mode')).toBe('mtuMode');
    expect(profileFieldOf('parts/impairment/jitter')).toBe('jitter');
    expect(profileFieldOf('parts/impairment/download/loss', true)).toBe('download.loss');
    expect(profileFieldOf('parts/impairment/burst_loss/r')).toBe('burstR');
    expect(profileFieldOf('profile')).toBeUndefined();
  });
});
