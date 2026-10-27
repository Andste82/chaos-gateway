// Probes in the UI (plan §2.12, §2.17 "Probes"; M25).
import type { ProbeView } from '../api/generated/model';
import type { DotTone } from '../components/ui/StatusDot.vue';

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
