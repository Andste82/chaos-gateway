<script setup lang="ts">
// Device detail (plan §2.17) with the data of Phase 2: identity, DHCP lease, active faults with
// their parameters, affected packets and remaining TTL, and the counters. "Add fault" starts where
// the device is (the fault dialog with the device as its fixed target), a fault of the device can
// be edited, "Apply profile" leads to the profiles with the device as the scope. The captures of the device
// are listed with a "Capture" action (M23a). The DHCP test actions (short lease, delete lease, force a new
// IP, set options, silence) are made and listed in the DHCP card beside the lease (M23b); a DNS fault or a TLS
// case of the device is made in the fault dialog and edited where it is listed. The panels whose backend comes
// later (flows view, diagnostics) arrive with their milestones; the HTTP exchanges of an interception case that acts on the device are listed in
// a panel (M23c). The port forwards to the device (M35a)
// are listed with their status and counter, each with a link to edit it on the networks page, which also
// adds one to the device.
// A configured device is edited (name, network, fixed IP) in the device dialog (#73); the Network card
// shows its fixed IP as the draft has it, with what the gateway refused of it. The header shows the
// name the list shows (a discovered device's host name, #78), the ADOPTED badge of a configured device,
// and "Remove" takes a configured device out of the configuration (references listed first), with an
// undo while that is not applied.
import { useQuery } from '@tanstack/vue-query';
import { computed, ref, useId } from 'vue';
import { ApiError } from '../api/fetcher';
import { useGetDevice } from '../api/generated/hooks/devices/devices';
import { listOverlays } from '../api/generated/hooks/overlays/overlays';
import type { Overlay } from '../api/generated/model';
import AdoptDialog from '../components/devices/AdoptDialog.vue';
import DeviceDialog from '../components/devices/DeviceDialog.vue';
import RemoveDeviceDialog from '../components/devices/RemoveDeviceDialog.vue';
import CapturePanel from '../components/captures/CapturePanel.vue';
import DeviceExchanges from '../components/intercept/DeviceExchanges.vue';
import DhcpActionsCard from '../components/dhcp/DhcpActionsCard.vue';
import ActiveFaultList from '../components/faults/ActiveFaultList.vue';
import FaultDialog from '../components/faults/FaultDialog.vue';
import PageHeader from '../components/layout/PageHeader.vue';
import LinuxObjectExcerpt from '../components/linux/LinuxObjectExcerpt.vue';
import DisclosureButton from '../components/ui/DisclosureButton.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadError from '../components/ui/LoadError.vue';
import SectionCard from '../components/ui/SectionCard.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import TestCaLink from '../components/ui/TestCaLink.vue';
import { fetchAll, firstError, useActiveConfig, useConfigFaults, useGatewayState, useNames, usePortForwards, type ActiveFault } from '../composables/gateway';
import { counterHint, counterText, entryLabels, forwardDeviceProblem, forwardRows, forwardSummary, forwardTitle, rowStatus } from '../lib/forwards';
import { useNow } from '../composables/useNow';
import { deviceNetworkInfo, deviceStatus, displayName, isAdopted, isConfigured, nameDetail, originLabel, sourcesLabel } from '../lib/devices';
import { describeMatch, describeScope, faultChips, overlayChips } from '../lib/faults';
import { focusAfterUpdate } from '../lib/focus';
import { profileTitle } from '../lib/profileNames';
import { dhcpServed } from '../lib/networks';
import { useDraftStore } from '../stores/draft';
import { formatAgo, formatByteRate, formatClock, formatRemaining, subnetOf } from '../lib/format';
import { useApiScreen } from '../stores/apiPanel';
import { useConnectionStore } from '../stores/connection';

const props = defineProps<{ id: string }>();

// "Show Linux view" (plan §2.17 "Technical view", P3-M14-03): the device's identity map elements and the
// classification of its addresses, as the kernel holds them
const linuxOpen = ref(false);
const linuxId = `${useId()}-linux`;

const device = useGetDevice(() => props.id, undefined, { query: { refetchInterval: 5_000 } });
const overlays = useQuery({
  queryKey: ['overlays', 'device', computed(() => props.id)],
  queryFn: ({ signal }) => fetchAll<Overlay>((cursor) => listOverlays({ device: props.id, limit: 500, cursor }, { signal }), signal),
  refetchInterval: 5_000,
});
const configFaults = useConfigFaults();
const { names, networks } = useNames();
const state = useGatewayState();
const draft = useDraftStore();
const connection = useConnectionStore();
const now = useNow();
const forwardViews = usePortForwards();
// the draft's base: the forwards as the draft has them; until it is loaded the card does not say "none"
const active = useActiveConfig();

const d = computed(() => device.data.value);
const notFound = computed(() => device.error.value instanceof ApiError && device.error.value.status === 404);
const network = computed(() => networks.data.value?.find((n) => n.id === d.value?.network));
const networkConfig = computed(() => network.value?.config as { name?: string; address?: string } | undefined);
const netInfo = computed(() => (d.value ? deviceNetworkInfo(network.value?.config, d.value.id) : undefined));
/** The device is in no network the gateway serves (a WireGuard client has none: its card is left out). */
const noNetwork = computed(() => !!networks.data.value && !network.value);
/** DHCP is off where the device is: the gateway's rule, a network without a `dhcp` block included. */
const dhcpOff = computed(() => !!networks.data.value && !!network.value && !dhcpServed(networkConfig.value as { dhcp?: { enabled?: boolean } } | undefined));
/** The DHCP server knows a device by its MAC addresses, the configured ones or the seen ones. */
const hasMac = computed(() => !!d.value && ((d.value.observed.macs?.length ?? 0) > 0 || (d.value.config?.identifiers?.macs?.length ?? 0) > 0));
const groupNames = computed(() => (d.value?.groups ?? []).map((g) => names.value.group?.(g) ?? g));

const identity = computed(() => {
  const v = d.value;
  if (!v) return '';
  return [
    v.observed.addresses?.join(', '),
    v.observed.macs?.join(', '),
    networkConfig.value?.name ? `${networkConfig.value.name} network` : undefined,
    groupNames.value.length ? `group ${groupNames.value.join(', ')}` : undefined,
  ]
    .filter(Boolean)
    .join(' · ');
});

/** An interception case acts on the device (its own or a wider scope's): its exchanges get a panel (M23c). */
const intercepted = computed(() => (overlays.data.value ?? []).some((o) => o.kind === 'tls' && o.tls?.case === 'intercept'));

const faults = computed<ActiveFault[]>(() => {
  const n = names.value;
  const out: ActiveFault[] = [];
  for (const o of overlays.data.value ?? []) {
    if (o.kind === 'rule') continue;
    const own = o.target?.device === d.value?.id;
    out.push({
      id: o.id,
      layer: 'overlay',
      kind: o.kind,
      target: own ? (d.value?.name ?? '') : describeScope(o.target, n),
      destination: [own ? '' : `via ${describeScope(o.target, n)}`, describeMatch(o.fault, n)].filter(Boolean).join(' '),
      chips: overlayChips(o, n.profile, n),
      packets: o.counters?.packets,
      expiresAt: o.expires_at,
      state: o.state,
      overlay: o,
      widerScope: own ? undefined : describeScope(o.target, n),
    });
  }
  // configured faults that win for this device (the overlays are listed above already), with the
  // compact chips of their parameters when the configured fault is at hand
  const configured = new Map((configFaults.data.value ?? []).map((f) => [f.id, f]));
  for (const f of d.value?.effective ?? []) {
    if (f.layer !== 'config') continue;
    const cf = configured.get(f.id);
    const chips = f.profile?.name ? [`profile ${profileTitle(f.profile.name)}`] : cf ? faultChips(cf.config) : [f.summary ?? f.family];
    out.push({
      id: f.id,
      layer: 'config',
      kind: 'fault',
      target: cf?.config.source ? describeScope(cf.config.source, n) : (f.name ?? ''),
      destination: [f.name ? `fault ${f.name}` : '', cf ? describeMatch(cf.config, n) : ''].filter(Boolean).join(' '),
      chips,
      packets: cf?.counters?.packets,
    });
  }
  return out;
});

const lease = computed(() => d.value?.observed.lease);
const leaseLeft = computed(() => (lease.value ? new Date(lease.value.expires_at).getTime() - now.value.getTime() : 0));

const adoptOpen = ref(false);
/** The name the device is adopted under in the draft, while that is not applied yet. */
const adoptedAs = computed(() => (d.value && !isAdopted(d.value) ? (draft.devices[d.value.id]?.name ?? undefined) : undefined));
function undoAdoption() {
  if (d.value) draft.setDevice(d.value.id, null);
  focusAfterUpdate('[data-focus-key="adopt"]');
}
const editOpen = ref(false);
const removeOpen = ref(false);
/** A configured device whose removal from the configuration is in the draft, not applied yet. */
const removed = computed(() => !!d.value && isConfigured(d.value) && draft.devices[d.value.id] === null);
function undoRemoval() {
  if (d.value) draft.restoreDevice(d.value.id);
  focusAfterUpdate('[data-focus-key="remove-device"]');
}
/** The configured device as the draft has it (its edit or the configuration); undefined for any other kind or a deletion. */
const configured = computed(() => {
  const v = d.value;
  if (!v || v.origin !== 'configured' || draft.devices[v.id] === null) return undefined;
  return draft.devices[v.id] ?? draft.baseConfig?.devices?.[v.id] ?? v.config;
});
/** The fixed IP the draft gives the device, while it differs from the applied one. */
const fixedIpPending = computed(() => !!d.value && d.value.id in draft.devices && (configured.value?.fixed_ip ?? '') !== (d.value.config?.fixed_ip ?? ''));
/** What the gateway refused of the device in the last candidate, until it is edited. */
const deviceRefused = computed(() => (d.value ? Object.values(draft.serverErrors[d.value.id] ?? {}) : []));
const faultOpen = ref(false);
const editing = ref<Overlay>();
function addFault() {
  editing.value = undefined;
  faultOpen.value = true;
}
function editFault(o: Overlay) {
  editing.value = o;
  faultOpen.value = true;
}
/** The port forwards to this device, as the draft has them. */
const forwards = computed(() => forwardRows(draft.baseForwards, draft.forwards, forwardViews.data.value ?? []).filter((r) => r.p.target.device === d.value?.id));
/**
 * A forward can go to the device: it is configured (or adopted in the draft), not a WireGuard client and
 * not configured in another kind of network (one without a network is a target). The gateway refuses a
 * forward to another device; the hint says what to do instead.
 */
const forwardBlocked = computed(() => {
  const v = d.value;
  if (!v) return undefined;
  // the networks not loaded yet: the dialog and the gateway check
  const problem = forwardDeviceProblem(v, draft.devices[v.id], (id) => networks.data.value?.find((n) => n.id === id)?.config.type);
  if (problem === 'discovered') return 'Adopt the device to forward to it, or forward to its address on the networks page.';
  if (problem === 'not_lan') return 'A port forward goes to a device in a local test network.';
  return undefined;
});
const forwardDevice = (id: string) => (id === d.value?.id ? d.value.name : names.value.device?.(id));
const changeBlocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);

useApiScreen('this device', () => [
  { method: 'GET', path: `/devices/${props.id}`, note: 'identity, lease, winning faults, counters' },
  { method: 'GET', path: `/overlays?device=${props.id}`, note: "the overlays that act on the device, its network's and groups' included" },
  { method: 'GET', path: '/port-forwards', note: 'the port forwards, those to this device listed' },
  { method: 'GET', path: '/captures', note: 'the captures, those of this device listed' },
]);
</script>

<template>
  <p v-if="device.isLoading.value" class="text-text-2">Loading the device…</p>
  <div v-else-if="notFound" class="card" role="alert">
    <h1 class="m-0 text-xl font-semibold">No such device</h1>
    <p class="text-text-2">The gateway knows no device “{{ id }}”. A discovered device that left may be gone.</p>
    <RouterLink to="/devices">All devices</RouterLink>
  </div>
  <div v-else-if="!d && device.error.value" class="flex flex-col gap-4">
    <nav aria-label="Breadcrumb" class="text-[13px] text-text-2"><RouterLink to="/devices">Devices</RouterLink> / {{ id }}</nav>
    <LoadError :error="device.error.value" what="The device is not loaded" :retry="device.refetch" />
  </div>
  <template v-else-if="d">
    <PageHeader :title="displayName(d)">
      <template #breadcrumb>
        <nav aria-label="Breadcrumb" class="mb-1 text-[13px] text-text-2"><RouterLink to="/devices">Devices</RouterLink> / {{ displayName(d) }}</nav>
      </template>
      <template #actions>
        <button type="button" class="btn btn-primary" :disabled="changeBlocked" @click="addFault">Add fault</button>
        <RouterLink class="btn" :to="{ path: '/profiles', query: { scope: `device:${d.id}` } }">Apply profile</RouterLink>
        <button v-if="configured" type="button" class="btn" :disabled="changeBlocked" data-focus-key="edit-device" @click="editOpen = true">Edit</button>
        <button
          v-if="configured"
          type="button"
          class="btn"
          :disabled="changeBlocked"
          :aria-label="`Remove ${d.name} from the configuration`"
          data-focus-key="remove-device"
          @click="removeOpen = true"
        >
          Remove
        </button>
        <button v-if="removed" type="button" class="btn" :aria-label="`Undo the removal of ${d.name}`" @click="undoRemoval">Undo removal</button>
        <button v-if="adoptedAs" type="button" class="btn" :aria-label="`Undo the adoption of ${d.name}`" @click="undoAdoption">Undo adoption</button>
        <button
          v-else-if="!isAdopted(d)"
          type="button"
          class="btn"
          :disabled="changeBlocked"
          data-focus-key="adopt"
          @click="adoptOpen = true"
        >
          Adopt
        </button>
      </template>
    </PageHeader>
    <div class="-mt-2 flex flex-wrap items-center gap-3 text-sm" data-testid="device-identity">
      <StatusDot v-bind="deviceStatus(d)" />
      <span
        v-if="isConfigured(d)"
        class="rounded border border-border-strong px-1.5 text-xs text-text-2"
        title="In the configuration: adopted here, or configured via YAML or the API"
        data-testid="device-adopted-badge"
        >ADOPTED</span
      >
      <span v-if="nameDetail(d)" class="mono text-text-3" data-testid="device-name-detail">{{ nameDetail(d) }}</span>
      <span class="mono text-text-2">{{ identity }}</span>
      <span v-if="adoptedAs" class="rounded border border-accent-strong px-1.5 text-xs text-accent-text" data-testid="device-adopted-mark"
        >ADOPTED as {{ adoptedAs }} · not applied yet</span
      >
      <span v-if="removed" class="rounded border border-block-border px-1.5 text-xs text-block-text" data-testid="device-removed-mark"
        >REMOVED · not applied yet</span
      >
    </div>

    <div class="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
      <div class="flex min-w-0 flex-col gap-5">
        <SectionCard title="Active faults">
          <p v-if="overlays.isLoading.value" class="m-0 text-sm text-text-2">Loading…</p>
          <LoadError
            v-else-if="firstError(overlays)"
            :error="firstError(overlays)"
            what="The active faults are not loaded"
            :retry="overlays.refetch"
          />
          <EmptyState v-else-if="faults.length === 0" text="No active faults: this device's traffic passes unimpaired.">
            <button type="button" class="btn btn-primary" :disabled="changeBlocked" @click="addFault">Add a fault</button>
          </EmptyState>
          <ActiveFaultList v-else :items="faults" removable editable :remove-disabled="changeBlocked" @edit="editFault" />
        </SectionCard>

        <SectionCard title="Traffic">
          <dl class="m-0 grid grid-cols-[160px_1fr] gap-x-4 gap-y-2 text-sm" data-testid="device-counters">
            <dt class="text-text-2">Upload</dt>
            <dd class="mono m-0">{{ formatByteRate(d.observed.upload_bps) }}</dd>
            <dt class="text-text-2">Download</dt>
            <dd class="mono m-0">{{ formatByteRate(d.observed.download_bps) }}</dd>
            <dt class="text-text-2">Active connections</dt>
            <dd class="mono m-0">{{ d.observed.flows_active ?? 0 }}</dd>
            <dt class="text-text-2">Last seen</dt>
            <dd class="m-0">
              <span class="mono">{{ d.observed.last_seen ? formatClock(d.observed.last_seen) : '—' }}</span>
              <span v-if="d.observed.last_seen" class="text-text-3"> · {{ formatAgo(d.observed.last_seen, now) }}</span>
            </dd>
          </dl>
        </SectionCard>

        <DeviceExchanges :device="{ id: d.id, name: d.name }" :active="intercepted" />

        <CapturePanel :device="{ id: d.id, name: d.name, network: d.network }" />
      </div>

      <aside class="flex min-w-0 flex-col gap-5">
        <SectionCard title="Network">
          <dl class="m-0 grid grid-cols-[100px_1fr] gap-x-4 gap-y-2 text-sm" data-testid="device-lease">
            <dt class="text-text-2">Network</dt>
            <dd class="m-0" data-testid="device-network">
              <RouterLink v-if="network && networkConfig?.name" :to="`/networks/${network.id}`">{{ networkConfig.name }}</RouterLink>
              <template v-else>—</template>
              <template v-if="networkConfig?.address">
                {{ ' · ' }}<span class="mono text-text-2">{{ subnetOf(networkConfig.address) }}</span>
              </template>
            </dd>
            <dt class="text-text-2">Lease</dt>
            <dd class="m-0">
              <template v-if="lease">
                <span class="mono">{{ lease.ip }}</span> · {{ lease.state ?? 'active' }}
                <template v-if="leaseLeft > 0"> · <span class="mono">{{ formatRemaining(leaseLeft) }}</span> left</template>
                <template v-else> · expired</template>
                <span v-if="lease.hostname" class="block text-text-3">host name {{ lease.hostname }}</span>
              </template>
              <template v-else>no DHCP lease</template>
            </dd>
            <template v-if="configured">
              <dt class="text-text-2">Fixed IP</dt>
              <dd class="m-0" data-testid="device-fixed-ip">
                <span class="mono" data-testid="device-fixed-ip-value">{{ configured.fixed_ip || 'none' }}</span>
                <span
                  v-if="fixedIpPending"
                  class="ml-1.5 rounded border border-accent-strong px-1.5 text-xs text-accent-text"
                  data-testid="device-fixed-ip-mark"
                  >not applied yet</span
                >
                <button
                  type="button"
                  class="btn btn-sm ml-2"
                  :disabled="changeBlocked"
                  :aria-label="configured.fixed_ip ? `Edit the fixed IP of ${d.name}` : `Set a fixed IP for ${d.name}`"
                  @click="editOpen = true"
                >
                  {{ configured.fixed_ip ? 'Edit' : 'Set' }}
                </button>
              </dd>
            </template>
            <template v-if="netInfo?.gateway">
              <dt class="text-text-2">Gateway</dt>
              <dd class="mono m-0" data-testid="device-gateway">{{ netInfo.gateway }}</dd>
            </template>
            <template v-if="netInfo">
              <dt class="text-text-2">DNS</dt>
              <dd class="m-0" data-testid="device-dns">
                <span class="mono">{{ netInfo.dns.join(', ') || 'none' }}</span>
                <span v-if="netInfo.proxy" class="text-text-2"> (proxy)</span>
              </dd>
            </template>
          </dl>
          <p v-if="deviceRefused.length" class="field-error mt-2 mb-0" role="alert" data-testid="device-refused">
            The gateway refused the change: {{ deviceRefused.join(' ') }}
            <button type="button" class="btn btn-sm ml-1" :aria-label="`Fix ${d.name}`" @click="editOpen = true">Fix</button>
          </p>
        </SectionCard>

        <DhcpActionsCard
          v-if="network?.config.type !== 'wireguard'"
          :target="{ device: d.id }"
          :name="d.name"
          :dhcp-off="dhcpOff"
          :no-network="noNetwork"
          :no-mac="!hasMac"
        />

        <SectionCard title="Port forwards">
          <template #actions>
            <RouterLink v-if="!forwardBlocked" class="btn btn-sm" :to="{ path: '/networks', query: { forward: 'new', device: d.id } }" :aria-label="`Add a port forward to ${d.name}`">Add</RouterLink>
          </template>
          <LoadError v-if="firstError(forwardViews)" :error="firstError(forwardViews)" what="The status of the port forwards is not loaded" :retry="forwardViews.refetch" />
          <template v-if="!draft.baseConfig">
            <LoadError v-if="firstError(active)" :error="firstError(active)" what="The port forwards are not loaded" :retry="active.refetch" />
            <p v-else class="m-0 text-sm text-text-2" role="status" data-testid="device-forwards-loading">Loading the port forwards…</p>
          </template>
          <p v-else-if="forwards.length === 0" class="m-0 text-sm text-text-2" data-testid="device-forwards-empty">No port forward leads to this device.</p>
          <ul v-else class="m-0 flex list-none flex-col gap-2 p-0" data-testid="device-forwards">
            <li v-for="r in forwards" :key="r.id" class="text-[13px]" data-testid="device-forward">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span class="mono" :class="r.mark === 'DELETED' ? 'line-through opacity-60' : ''">{{ forwardSummary(r.p, forwardDevice) }}</span>
                <span class="text-text-2">from the {{ entryLabels[r.p.entry] }}</span>
                <span
                  v-if="r.mark"
                  class="rounded border px-1.5 text-xs"
                  :class="r.mark === 'DELETED' ? 'border-block-border text-block-text' : 'border-accent-strong text-accent-text'"
                  data-testid="device-forward-mark"
                  >{{ r.mark }}</span
                >
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
                <StatusDot v-bind="rowStatus(r)" />
                <span v-if="counterText(r)" class="mono text-xs text-text-2" :title="counterHint">{{ counterText(r) }}</span>
                <RouterLink
                  v-if="r.mark !== 'DELETED'"
                  :to="{ path: '/networks', query: { forward: r.id } }"
                  class="text-[13px]"
                  :aria-label="`Edit the port forward ${forwardTitle(r.p)}`"
                  >Edit</RouterLink
                >
              </div>
            </li>
          </ul>
          <p v-if="forwardBlocked" class="mt-2 mb-0 text-xs text-text-2" data-testid="device-forwards-blocked">{{ forwardBlocked }}</p>
          <p class="mt-2 mb-0 text-xs text-text-3">Faults do not apply to forwarded connections; the access rules do.</p>
        </SectionCard>

        <SectionCard title="Identity">
          <dl class="m-0 grid grid-cols-[100px_1fr] gap-x-4 gap-y-2 text-sm">
            <dt class="text-text-2">Origin</dt>
            <dd class="m-0">{{ originLabel(d) }}</dd>
            <dt class="text-text-2">MAC</dt>
            <dd class="mono m-0">{{ d.observed.macs?.join(', ') || '—' }}</dd>
            <dt class="text-text-2">Addresses</dt>
            <dd class="mono m-0">{{ d.observed.addresses?.join(', ') || '—' }}</dd>
            <dt class="text-text-2">Seen by</dt>
            <dd class="m-0">{{ sourcesLabel(d) || '—' }}</dd>
            <template v-if="groupNames.length">
              <dt class="text-text-2">Groups</dt>
              <dd class="m-0">{{ groupNames.join(', ') }}</dd>
            </template>
            <template v-if="d.config">
              <dt class="text-text-2">Test CA</dt>
              <dd class="m-0">{{ d.config.trusts_test_ca ? 'trusted' : 'not trusted' }} · <TestCaLink /></dd>
            </template>
          </dl>
          <DisclosureButton v-model:open="linuxOpen" :controls="linuxId" class="mt-3">
            {{ linuxOpen ? 'Hide Linux view' : 'Show Linux view (identity and classification)' }}
          </DisclosureButton>
          <div v-if="linuxOpen" :id="linuxId" class="mt-2"><LinuxObjectExcerpt kind="device" :id="d.id" /></div>
        </SectionCard>
      </aside>
    </div>

    <AdoptDialog v-model:open="adoptOpen" :device="d" :network-name="networkConfig?.name" />
    <DeviceDialog v-if="configured" v-model:open="editOpen" :device="d" />
    <RemoveDeviceDialog v-if="isConfigured(d)" v-model:open="removeOpen" :device="d" />
    <FaultDialog v-model:open="faultOpen" :fixed-target="`device:${d.id}`" :overlay="editing" />
  </template>
</template>
