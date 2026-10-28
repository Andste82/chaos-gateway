// Probes in the UI (plan §2.12, §2.17 "Probes"; M25, M26a): the state of a probe and the choices of the diagnostic forms (M25),
// and the form of a probe (name, network, trusts the test CA), its checks under the fields, the probe as the configuration stores
// it, the gateway's messages by field, the probe in words for the unapplied-changes bar, and the rows of the list: the probes of
// the draft with the address the gateway observed for them (M26a).
import type { Probe, ProbeView } from '../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import type { DotTone } from '../components/ui/StatusDot.vue';
import { cloneJSON, jsonEqual } from './mergePatch';
import type { FieldChange } from './rules';

/** The state of a probe in words: without an address it waits for its DHCP lease, with one it is online or has gone quiet. */
export function probeTone(p: Pick<ProbeView, 'observed'>): { tone: DotTone; label: string } {
  if (!(p.observed.addresses ?? []).length) return { tone: 'unknown', label: 'No address yet' };
  return p.observed.online ? { tone: 'normal', label: 'Online' } : { tone: 'offline', label: 'Offline' };
}

/** A probe by its id or name (the form of a diagnostic and the link of a device's page name it either way). */
export function findProbe(probes: ProbeView[], ref: string): ProbeView | undefined {
  const r = ref.trim().toLowerCase();
  return probes.find((p) => p.id.toLowerCase() === r || p.config.name.toLowerCase() === r);
}

/** The profiles a calibration can measure: those with an impairment part. */
export function measurableProfiles(profiles: { id: string; config: { name: string; parts: { impairment?: unknown } }; available?: boolean }[]): { id: string; name: string }[] {
  return profiles.filter((p) => p.config.parts.impairment).map((p) => ({ id: p.id, name: p.config.name }));
}

// ---- the form ----------------------------------------------------------------------------------

export interface ProbeForm {
  name: string;
  /** The local test network by id. */
  network: string;
  trustsTestCa: boolean;
}

export type ProbeField = 'name' | 'network';

export function emptyProbeForm(network = ''): ProbeForm {
  return { name: '', network, trustsTestCa: false };
}

export function formFromProbe(p: Probe): ProbeForm {
  return { name: p.name, network: p.network, trustsTestCa: p.trusts_test_ca === true };
}

/** What the checks of the form need to know of the rest of the configuration. */
export interface ProbeContext {
  /** The names of the other devices, WireGuard clients and probes of the draft: they share one namespace. */
  taken: string[];
  /** The local test networks a probe can be a port of. */
  networks: { id: string; name: string }[];
}

const uuidShape = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** The checks of the probe form, each under its field (plan §2.17 "Validation"); the gateway checks the rest. */
export function probeFormErrors(f: ProbeForm, ctx: ProbeContext): Partial<Record<ProbeField, string>> {
  const e: Partial<Record<ProbeField, string>> = {};
  const name = f.name.trim();
  if (!name) e.name = 'The name is required.';
  else if (!new RegExp(nameRegExp).test(name)) e.name = 'Letters, digits, dot, dash and underscore; it starts with a letter or digit (at most 63).';
  else if (uuidShape.test(name)) e.name = 'A name must not have the form of a UUID.';
  else if (ctx.taken.some((t) => t.toLowerCase() === name.toLowerCase())) e.name = `${name} is already the name of a device, a WireGuard client or a probe.`;
  if (!f.network) e.network = 'Choose the test network the probe is a port of.';
  else if (!ctx.networks.some((n) => n.id === f.network)) e.network = 'A probe is a port of a local test network: choose one.';
  return e;
}

/**
 * The probe the form describes, as the configuration stores it; `before` is the probe as the draft has it, and what the
 * form does not edit (description, labels) stays. The switch is written when it is on, and kept off explicitly when the
 * base had it so.
 */
export function probeFromForm(f: ProbeForm, before?: Probe): Probe {
  const p: Probe = cloneJSON(before) ?? { name: '', network: '' };
  p.name = f.name.trim();
  p.network = f.network;
  if (f.trustsTestCa) p.trusts_test_ca = true;
  else if (before?.trusts_test_ca === false) p.trusts_test_ca = false;
  else delete p.trusts_test_ca;
  return p;
}

/** The field of the form for a message of the gateway, by its pointer below /probes/{id}. */
export function probeFieldOf(pointer: string): ProbeField | undefined {
  const first = pointer.split('/')[0];
  return first === 'name' || first === 'network' ? first : undefined;
}

// ---- in words ----------------------------------------------------------------------------------

/** What a probe is called in the changes and the lists: its name. */
export function probeTitle(p: Probe): string {
  return p.name;
}

export type NetworkName = (id: string) => string;

/** The changes of one probe in words: added, deleted, or field by field (the unapplied-changes bar). */
export function probeChanges(before: Probe | undefined, after: Probe | null, networkName: NetworkName = (id) => id): FieldChange[] {
  if (after === null) return before ? [{ field: 'probe', from: 'present', to: 'deleted' }] : [];
  if (!before) return [{ field: 'probe', from: '—', to: `added (on ${networkName(after.network)}${after.trusts_test_ca ? ', trusts the test CA' : ''})` }];
  const out: FieldChange[] = [];
  const add = (field: string, a: string, b: string) => a !== b && out.push({ field, from: a || '—', to: b || '—' });
  add('name', before.name, after.name);
  add('network', networkName(before.network), networkName(after.network));
  add('trusts the test CA', before.trusts_test_ca ? 'yes' : 'no', after.trusts_test_ca ? 'yes' : 'no');
  // a change the words above do not show (the draft keeps no edit that equals the base)
  if (!out.length && !jsonEqual(before, after)) out.push({ field: 'settings', from: 'as before', to: 'changed' });
  return out;
}

// ---- the rows of the list ----------------------------------------------------------------------

export interface ProbeRow {
  id: string;
  /** The probe as the draft has it (as the base had it, for a deleted one). */
  config: Probe;
  /** What the gateway observes of the applied probe: its address, MAC and state. None for a probe not applied yet. */
  view?: ProbeView;
  mark?: 'NEW' | 'CHANGED' | 'DELETED';
}

/**
 * The probes to list: those of the base revision with the draft's edits, the ones the draft adds, and the deleted ones
 * (marked), each with the gateway's view of it; sorted by name. A probe the gateway lists and the base does not have yet
 * (the configuration is not loaded) is listed as the gateway has it.
 */
export function probeRows(base: Record<string, Probe>, edits: Record<string, Probe | null>, views: ProbeView[]): ProbeRow[] {
  const byId = new Map(views.map((v) => [v.id, v]));
  const rows: ProbeRow[] = [];
  for (const id of new Set([...Object.keys(base), ...Object.keys(edits)])) {
    const was = base[id];
    const edit = edits[id];
    if (edit === null) {
      if (was) rows.push({ id, config: was, view: byId.get(id), mark: 'DELETED' });
      continue;
    }
    const config = edit ?? was!;
    const mark = !was ? 'NEW' : edit && !jsonEqual(was, edit) ? 'CHANGED' : undefined;
    rows.push({ id, config, view: byId.get(id), mark });
  }
  for (const v of views) if (!rows.some((r) => r.id === v.id)) rows.push({ id: v.id, config: v.config, view: v });
  return rows.sort((a, b) => a.config.name.localeCompare(b.config.name, undefined, { numeric: true }));
}

/**
 * The state of a row in words: the probe's own (address, online) from the gateway; a probe not applied yet has none, and a
 * changed one says that its change is not applied yet (the gateway's view describes the applied probe).
 */
export function rowTone(r: ProbeRow): { tone: DotTone; label: string } {
  if (r.mark === 'NEW') return { tone: 'offline', label: 'not applied yet' };
  if (!r.view) return { tone: 'unknown', label: r.mark === 'CHANGED' ? 'status unknown · not applied yet' : 'status unknown' };
  const t = probeTone(r.view);
  return r.mark === 'CHANGED' ? { tone: t.tone, label: `${t.label} · change not applied yet` } : t;
}
