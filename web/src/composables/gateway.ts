// The server data the shell and the screens share, as Vue Query queries (plan §3.7: server data
// stays in the query cache, the event stream invalidates it). Lists follow the cursor to the end,
// so a lab with many devices shows all of them. The keys start like the generated hooks' keys, so
// the invalidation by event type (eventStream.ts) reaches them.
import { useQueries, useQuery } from '@tanstack/vue-query';
import { computed, watch } from 'vue';
import { listDevices, listInterceptFlows, listLeases } from '../api/generated/hooks/devices/devices';
import { listWireGuardClients } from '../api/generated/hooks/wireguard/wireguard';
import { listAccessRules, listFaults, listGroups, listNetworks, listPortForwards, listProfiles, listScenarios } from '../api/generated/hooks/config/config';
import { getCaptureStorage, listCaptures } from '../api/generated/hooks/captures/captures';
import { getRun, listRuns } from '../api/generated/hooks/runs/runs';
import { listOverlays } from '../api/generated/hooks/overlays/overlays';
import { getActiveRevision, getGetActiveRevisionQueryKey } from '../api/generated/hooks/revisions/revisions';
import { getCapabilities, useGetHealth, useGetState, useGetSystemInfo } from '../api/generated/hooks/system/system';
import type { AccessRuleView, Capture, DeviceView, InterceptFlowPage, ListInterceptFlowsParams, DhcpLease, FaultView, GroupView, ListRunsParams, NetworkView, Overlay, PortForwardView, ProfileView, Run, ScenarioView, SystemAccessRule, WireGuardClientView } from '../api/generated/model';
import { describeMatch, describeScope, describeTunnel, faultChips, overlayChips, type Names } from '../lib/faults';
import { scopeLink } from '../lib/links';
import { isRunOf, mergeRuns } from '../lib/runs';
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

/** The port forwards of the active revision with their status and counter (M35a). */
export function usePortForwards() {
  return useQuery({
    queryKey: ['port-forwards', 'all'],
    queryFn: ({ signal }) => fetchAll<PortForwardView>((cursor) => listPortForwards({ limit: 500, cursor }, { signal }), signal),
    // the counters grow and a device target's address changes without an event of the forward
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

/** The scenarios of the active revision (M19). */
export function useScenarios() {
  return useQuery({
    queryKey: ['scenarios', 'all'],
    queryFn: ({ signal }) => fetchAll<ScenarioView>((cursor) => listScenarios({ limit: 500, cursor }, { signal }), signal),
  });
}

/** What this build can do: the step types and the check types a scenario may use (`GET /capabilities`). */
export function useCapabilities() {
  return useQuery({ queryKey: ['capabilities'], queryFn: ({ signal }) => getCapabilities({ signal }), staleTime: 5 * 60_000 });
}

/**
 * One page of the runs, newest first, without their timelines: the newest `limit` of those the filters select,
 * and the cursor of the next page when there are more (the gateway keeps the runs of 30 days, so the page is a
 * window: callers that must not miss a run filter on the gateway's side). A queued or running run changes
 * without an event of the list's own (the events of a run invalidate it), so the list is also polled.
 */
export function useRuns(params: () => Omit<ListRunsParams, 'cursor' | 'limit'> = () => ({}), limit: number | (() => number) = 200, enabled: () => boolean = () => true) {
  const size = () => Math.min(typeof limit === 'function' ? limit() : limit, 500);
  return useQuery({
    queryKey: computed(() => ['runs', 'all', params(), size()]),
    queryFn: ({ signal }) => listRuns({ ...params(), limit: size() }, { signal }),
    // asking for more of the same runs keeps the page that is shown; other filters start empty
    placeholderData: (prev, prevQuery) => (prevQuery && JSON.stringify(prevQuery.queryKey[2]) === JSON.stringify(params()) ? prev : undefined),
    enabled: computed(enabled),
    refetchInterval: 5_000,
  });
}

/** The runs that are queued or running, whatever the number of runs that came after them. */
export function useActiveRuns() {
  const running = useRuns(() => ({ status: 'running' }), 500);
  const queued = useRuns(() => ({ status: 'queued' }), 500);
  return {
    data: computed<Run[] | undefined>(() => (running.data.value && queued.data.value ? mergeRuns(running.data.value.items, queued.data.value.items) : undefined)),
    isLoading: computed(() => running.isLoading.value || queued.isLoading.value),
    error: computed(() => running.error.value ?? queued.error.value),
    refetch: () => Promise.all([running.refetch(), queued.refetch()]),
  };
}

/**
 * The runs of one scenario, newest first, asked for by the scenario: those of its id and, for the runs of an inline
 * scenario, those of its name. `limit` is the number asked for of each; `more` says the gateway has older ones.
 */
export function useScenarioRuns(scenario: () => { id: string; name: string } | undefined, limit: () => number, enabled: () => boolean = () => true) {
  const on = () => enabled() && !!scenario();
  const byId = useRuns(() => ({ scenario: scenario()?.id }), limit, on);
  const byName = useRuns(() => ({ scenario: scenario()?.name }), limit, on);
  return {
    data: computed<Run[] | undefined>(() => {
      const s = scenario();
      if (!s || !byId.data.value || !byName.data.value) return undefined;
      return mergeRuns(byId.data.value.items, byName.data.value.items).filter((r) => isRunOf(r, s));
    }),
    more: computed(() => !!byId.data.value?.next_cursor || !!byName.data.value?.next_cursor),
    isLoading: computed(() => byId.isLoading.value || byName.isLoading.value),
    error: computed(() => byId.error.value ?? byName.error.value),
    refetch: () => Promise.all([byId.refetch(), byName.refetch()]),
  };
}

/**
 * The newest run of each of the scenarios, asked for one by one (for those the latest runs of the list do not
 * reach); `null` where a scenario has none.
 */
export function useLastRuns(scenarios: () => { id: string; name: string }[]) {
  return useQueries({
    queries: computed(() =>
      scenarios().map((s) => ({
        queryKey: ['runs', 'last', s.id, s.name],
        queryFn: async ({ signal }: { signal: AbortSignal }): Promise<Run | null> => {
          const byId = await listRuns({ scenario: s.id, limit: 1 }, { signal });
          if (byId.items[0]) return byId.items[0];
          return (await listRuns({ scenario: s.name, limit: 1 }, { signal })).items[0] ?? null;
        },
      })),
    ),
  });
}

/**
 * One run with its timeline, checks and artifacts. While it is queued or running it is read every second
 * (the elapsed time and the next step move without an event); the events of the run read it at once.
 */
export function useRun(id: () => string | undefined) {
  return useQuery({
    queryKey: computed(() => ['runs', 'one', id()]),
    queryFn: ({ signal }) => getRun(id()!, { signal }),
    enabled: computed(() => !!id()),
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === 'queued' || s === 'running' ? 1_000 : false;
    },
  });
}

/**
 * The captures, newest first (M23a). The counters of a running capture grow without an event of their own (the
 * gateway reads them once a second), so the list is read every 2 s while one runs; the events of a capture read it
 * at once, and it is polled slowly otherwise.
 */
export function useCaptures(enabled: () => boolean = () => true) {
  return useQuery({
    queryKey: ['captures', 'all'],
    queryFn: ({ signal }) => fetchAll<Capture>((cursor) => listCaptures({ limit: 500, cursor }, { signal }), signal),
    enabled: computed(enabled),
    refetchInterval: (q) => (q.state.data?.some((c) => c.status === 'running') ? 2_000 : 15_000),
  });
}

/** What the captures take of the quota and of the volume, and how many run. */
export function useCaptureStorage(enabled: () => boolean = () => true) {
  return useQuery({
    queryKey: ['captures', 'storage'],
    queryFn: ({ signal }) => getCaptureStorage({ signal }),
    enabled: computed(enabled),
    refetchInterval: (q) => (q.state.data?.running ? 2_000 : 15_000),
  });
}

/**
 * Whether this deployment can capture: a gateway without a capture volume lacks the feature `captures`
 * and answers every capture operation with `unsupported_feature`. Undefined until the capabilities are known, so
 * nothing asks for captures before; capabilities that cannot be read do not keep the captures from being tried.
 */
export function useCanCapture() {
  const caps = useCapabilities();
  return computed<boolean | undefined>(() => {
    if (caps.data.value) return caps.data.value.features.includes('captures');
    return caps.error.value ? true : undefined;
  });
}

/**
 * Whether this gateway has the interception (M22): its features list `tls.intercept`. Undefined until the capabilities are known;
 * capabilities that cannot be read do not take the interception away (the gateway answers `unsupported_feature` itself).
 */
export function useCanIntercept() {
  const caps = useCapabilities();
  return computed<boolean | undefined>(() => {
    if (caps.data.value) return caps.data.value.features.includes('tls.intercept');
    return caps.error.value ? true : undefined;
  });
}

/**
 * The newest exchanges of the interception proxy that the parameters select (M23c). Exchanges that no rule acted on come without an
 * event, so the table is read every 3 s while `live` is on (the table that is not live holds what it has), and at once on the event
 * `http_fault_triggered`. A page is a window onto the newest exchanges: asking for more keeps the page that is shown.
 */
export function useInterceptFlows(params: () => ListInterceptFlowsParams, enabled: () => boolean = () => true, live: () => boolean = () => true) {
  return useQuery<InterceptFlowPage>({
    queryKey: computed(() => ['intercept', 'flows', params()]),
    queryFn: ({ signal }) => listInterceptFlows(params(), { signal }),
    enabled: computed(enabled),
    placeholderData: (prev, prevQuery) => {
      const { limit: _a, ...before } = (prevQuery?.queryKey[2] ?? {}) as ListInterceptFlowsParams;
      const { limit: _b, ...now } = params();
      return JSON.stringify(before) === JSON.stringify(now) ? prev : undefined;
    },
    refetchInterval: computed(() => (live() ? 3_000 : false)),
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
  /** The page of the target (a device, a network, else the faults screen): the target links to it. */
  link?: string;
  /** The device the fault targets, when it targets one. */
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
        link: scopeLink(o.target, o.fault?.tunnel),
        destination: describeMatch(o.fault, n),
        chips: overlayChips(o, n.profile, n),
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
        link: scopeLink(f.config.source, f.config.tunnel),
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
