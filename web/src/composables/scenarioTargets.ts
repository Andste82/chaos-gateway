// The choices of a scenario's target and of a step that narrows it (plan §2.10): a device, a group or a
// network — not everything and not a remote network. A scenario is an object of the configuration, so it
// can name only configured devices (a discovered device is adopted first). The index of the objects its
// references can name serves the YAML view, which shows names where the configuration stores UUIDs.
import { computed } from 'vue';
import type { WireGuardNetwork } from '../api/generated/model';
import type { Topology } from '../lib/scenarioRules';
import type { RefIndex } from '../lib/scenarioYaml';
import type { ScopeOption } from '../lib/scopes';
import { useGroups, useNames, useProfiles } from './gateway';

export function useScenarioTargets() {
  const { names, devices, networks } = useNames();
  const groups = useGroups();
  const options = computed<ScopeOption[]>(() => {
    const n = names.value;
    const out: ScopeOption[] = [];
    for (const d of devices.data.value ?? []) {
      if (d.origin === 'discovered') continue;
      out.push({ value: `device:${d.id}`, label: `Device · ${d.name}` });
    }
    for (const g of groups.data.value ?? []) out.push({ value: `group:${g.id}`, label: `Group · ${g.config.name}` });
    for (const net of networks.data.value ?? []) out.push({ value: `network:${net.id}`, label: `Network · ${n.network?.(net.id) ?? net.id}` });
    return out;
  });
  return { options, names, devices, networks, groups };
}

/**
 * What the rules about a scenario's target need of the configuration (`lib/scenarioRules.ts`): the network of
 * each device and WireGuard client, and the members of each group. Unknown until they are loaded.
 */
export function useTopology() {
  const t = useScenarioTargets();
  return computed<Topology>(() => {
    const network = new Map<string, string>();
    for (const d of t.devices.data.value ?? []) if (d.network) network.set(d.id, d.network);
    // a client of a hub is a device of the hub's network
    for (const n of t.networks.data.value ?? []) {
      if (n.config.type !== 'wireguard') continue;
      for (const id of Object.keys((n.config as WireGuardNetwork).clients ?? {})) network.set(id, n.id);
    }
    const members = new Map((t.groups.data.value ?? []).map((g) => [g.id, g.config.members ?? []]));
    return { networkOf: (id) => network.get(id), membersOf: (id) => members.get(id) };
  });
}

/** The objects a scenario's references can name, by name and UUID; empty until they are loaded. */
export function useRefIndex() {
  const t = useScenarioTargets();
  const profiles = useProfiles();
  return computed<RefIndex>(() => ({
    devices: (t.devices.data.value ?? []).map((d) => ({ id: d.id, name: d.name })),
    groups: (t.groups.data.value ?? []).map((g) => ({ id: g.id, name: g.config.name })),
    networks: (t.networks.data.value ?? []).map((n) => ({ id: n.id, name: (n.config as { name?: string }).name ?? n.id })),
    profiles: (profiles.data.value ?? []).map((p) => ({ id: p.id, name: p.config.name })),
  }));
}
