// The server data the shell and the screens share, as Vue Query queries (plan §3.7: server data
// stays in the query cache, the event stream invalidates it). Lists follow the cursor to the end,
// so a lab with many devices shows all of them. The keys start like the generated hooks' keys, so
// the invalidation by event type (eventStream.ts) reaches them.
import { useQuery } from '@tanstack/vue-query';
import { computed, watch } from 'vue';
import { listDevices, listLeases } from '../api/generated/hooks/devices/devices';
import { listWireGuardClients } from '../api/generated/hooks/wireguard/wireguard';
import { listAccessRules, listFaults, listGroups, listNetworks, listProfiles } from '../api/generated/hooks/config/config';
import { listOverlays } from '../api/generated/hooks/overlays/overlays';
import { getActiveRevision, getGetActiveRevisionQueryKey } from '../api/generated/hooks/revisions/revisions';
import { useGetHealth, useGetState, useGetSystemInfo } from '../api/generated/hooks/system/system';
import type { AccessRuleView, DeviceView, DhcpLease, FaultView, GroupView, NetworkView, Overlay, ProfileView, SystemAccessRule, WireGuardClientView } from '../api/generated/model';
import { describeMatch, describeScope, describeTunnel, faultChips, overlayChips, type Names } from '../lib/faults';
import { useDraftStore } from '../stores/draft';

interface PageOf<T> {
  items: T[];
  next_cursor?: string;
}

/** Every item of a paginated list. */
export async function fetchAll<T>(page: (cursor: string | undefined, signal?: AbortSignal) => Promise<PageOf<T>>, signal?: AbortSignal): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < 100; i++) {
    const p = await page(cursor, signal);
    out.push(...p.items);
    if (!p.next_cursor) break;
    cursor = p.next_cursor;
  }
  return out;
}

export const useGatewayState = () => useGetState({ query: { refetchInterval: 10_000 } });
export const useHealth = () => useGetHealth({ query: { refetchInterval: 15_000 } });
export const useSystemInfo = () => useGetSystemInfo({ query: { staleTime: 60_000 } });

export function useDevices() {
  return useQuery({
    queryKey: ['devices', 'all'],
    queryFn: ({ signal }) => fetchAll<DeviceView>((cursor) => listDevices({ limit: 500, cursor }, { signal }), signal),
    // traffic rates change without an event
    refetchInterval: 5_000,
  });
}

export function useNetworks() {
  return useQuery({
    queryKey: ['networks', 'all'],
    queryFn: ({ signal }) => fetchAll<NetworkView>((cursor) => listNetworks({ limit: 500, cursor }, { signal }), signal),
  });
}

/** The DHCP leases of a local network (plan §2.7: shown in the network view). */
export function useLeases(network: () => string | undefined) {
  return useQuery({
    queryKey: computed(() => ['networks', network(), 'leases', 'all']),
    queryFn: ({ signal }) => fetchAll<DhcpLease>((cursor) => listLeases(network()!, { limit: 500, cursor }, { signal }), signal),
    enabled: computed(() => !!network()),
    // a lease's remaining time runs down and renewals come without an event of their own
    refetchInterval: 10_000,
  });
}

/** The clients of a WireGuard hub with their status (last handshake, endpoint, bytes). */
export function useWireGuardClients(network: () => string | undefined) {
  return useQuery({
    queryKey: computed(() => ['networks', network(), 'clients', 'all']),
    queryFn: ({ signal }) => fetchAll<WireGuardClientView>((cursor) => listWireGuardClients(network()!, { limit: 500, cursor }, { signal }), signal),
    enabled: computed(() => !!network()),
    // the handshakes and the bytes change without an event
    refetchInterval: 5_000,
  });
}

export function useGroups() {
  return useQuery({
    queryKey: ['groups', 'all'],
    queryFn: ({ signal }) => fetchAll<GroupView>((cursor) => listGroups({ limit: 500, cursor }, { signal }), signal),
  });
}

export function useProfiles() {
  return useQuery({
    queryKey: ['profiles', 'all'],
    queryFn: ({ signal }) => fetchAll<ProfileView>((cursor) => listProfiles({ limit: 500, cursor }, { signal }), signal),
    staleTime: 60_000,
  });
}

export function useOverlays() {
  return useQuery({
    queryKey: ['overlays', 'all'],
    queryFn: ({ signal }) => fetchAll<Overlay>((cursor) => listOverlays({ limit: 500, cursor }, { signal }), signal),
    // the counters of affected packets grow without an event
    refetchInterval: 5_000,
  });
}

export function useConfigFaults() {
  return useQuery({
    queryKey: ['faults', 'all'],
    queryFn: ({ signal }) => fetchAll<FaultView>((cursor) => listFaults({ limit: 500, cursor }, { signal }), signal),
    refetchInterval: 5_000,
  });
}

/**
 * The active revision with its configuration: the base of the configuration draft (rules, faults).
 * The draft takes it as its base while it has no edits of its own.
 */
export function useActiveConfig() {
  const draft = useDraftStore();
  const q = useQuery({ queryKey: getGetActiveRevisionQueryKey(), queryFn: ({ signal }) => getActiveRevision({ signal }) });
  watch(
    () => q.data.value,
    (rev) => rev && draft.track(rev),
    { immediate: true },
  );
  return q;
}

/** The configured access rules with their counters, and the system rules in front of them. */
export function useAccessRules() {
  return useQuery({
    queryKey: ['rules', 'all'],
    queryFn: async ({ signal }) => {
      let system: SystemAccessRule[] = [];
      const items = await fetchAll<AccessRuleView>(async (cursor) => {
        const p = await listAccessRules({ limit: 500, cursor }, { signal });
        if (!cursor) system = p.system_rules ?? [];
        return p;
      }, signal);
      return { items, system };
    },
    // the hit counters grow without an event
    refetchInterval: 5_000,
  });
}

/** Name lookups for the UUIDs that overlays and faults refer to. */
export function useNames() {
  const devices = useDevices();
  const networks = useNetworks();
  const groups = useGroups();
  const profiles = useProfiles();
  const names = computed<Names & { profile: (id: string) => string | undefined }>(() => {
    const dev = new Map((devices.data.value ?? []).map((d) => [d.id, d.name]));
    const net = new Map(
      (networks.data.value ?? []).map((n) => [n.id, (n.config as { name?: string }).name ?? n.id]),
    );
    const grp = new Map((groups.data.value ?? []).map((g) => [g.id, g.config.name]));
    const prof = new Map<string, string>();
    for (const p of profiles.data.value ?? []) {
      prof.set(p.id, p.config.name);
      prof.set(p.config.name, p.config.name);
    }
    return {
      device: (id) => dev.get(id),
      network: (id) => net.get(id),
      group: (id) => grp.get(id),
      profile: (id) => prof.get(id),
    };
  });
  return { names, devices, networks };
}

interface QueryLike {
  data: { value: unknown };
  error: { value: unknown };
}

/** The error of the first query that failed without data to show, or null. */
export function firstError(...queries: QueryLike[]): unknown {
  for (const q of queries) if (q.data.value === undefined && q.error.value) return q.error.value;
  return null;
}

/** One active fault of the overview and the device detail: an overlay or a configured fault. */
export interface ActiveFault {
  id: string;
  layer: 'overlay' | 'config';
  kind: string;
  target: string;
  /** The device the fault targets, when it targets one (the row links to it). */
  device?: string;
  destination: string;
  chips: string[];
  packets?: number;
  expiresAt?: string;
  state?: string;
  overlay?: Overlay;
  /**
   * On a device's page: the wider scope (network, group) of an overlay that acts on more devices
   * than this one; removing it asks first.
   */
  widerScope?: string;
}

// overlay kinds that impair traffic; access rules are not faults (plan §2.4, D25)
const faultKinds = new Set(['fault', 'profile', 'dns', 'tls', 'dhcp', 'wireguard']);

/** The faults that act now: impairing overlays and the enabled configured faults, overlays first. */
export function useActiveFaults() {
  const overlays = useOverlays();
  const faults = useConfigFaults();
  const { names, devices, networks } = useNames();
  const items = computed<ActiveFault[]>(() => {
    const n = names.value;
    const out: ActiveFault[] = [];
    for (const o of overlays.data.value ?? []) {
      if (!faultKinds.has(o.kind)) continue;
      out.push({
        id: o.id,
        layer: 'overlay',
        kind: o.kind,
        target: o.target ? describeScope(o.target, n) : o.fault?.tunnel ? describeTunnel(o.fault.tunnel, n) : 'Everything',
        device: o.target?.device,
        destination: describeMatch(o.fault, n),
        chips: overlayChips(o, n.profile),
        packets: o.counters?.packets,
        expiresAt: o.expires_at,
        state: o.state,
        overlay: o,
      });
    }
    for (const f of faults.data.value ?? []) {
      if (f.config.enabled === false || f.state === 'disabled') continue;
      out.push({
        id: f.id,
        layer: 'config',
        kind: 'fault',
        target: f.config.source ? describeScope(f.config.source, n) : describeTunnel(f.config.tunnel, n),
        device: f.config.source?.device,
        destination: describeMatch(f.config, n),
        chips: faultChips(f.config),
        packets: f.counters?.packets,
        state: f.state,
      });
    }
    return out;
  });
  return {
    items,
    isLoading: computed(() => overlays.isLoading.value || faults.isLoading.value),
    /**
     * Why the list is not known: a source failed before it delivered anything. The list then must
     * not say "no faults"; a source that failed on a refetch keeps its last data.
     */
    error: computed(() => firstError(overlays, faults)),
    refetch: () => Promise.all([overlays.refetch(), faults.refetch()]),
    devices,
    networks,
  };
}
