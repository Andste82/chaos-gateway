<script setup lang="ts">
// The DHCP test actions of a device or a network (plan §2.7, §2.17 "Device detail" and "Networks", M23b): a button for each
// action (short lease, delete lease, force a new IP, set options, silence; a network has no "Force a new IP"), each opening
// the DHCP dialog with its action, and the list of the actions that are in force for exactly this device or network with
// their state, their remaining time and, for a forced new IP, the address the gateway moved the device to. An action can be
// edited and removed here. A network whose DHCP is off has nothing for an action to change, and a device without a MAC
// address cannot be moved (the gateway refuses `force_new_ip` for it), so their buttons are off and the card says why.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { deleteOverlay } from '../../api/generated/hooks/overlays/overlays';
import type { Overlay, Scope } from '../../api/generated/model';
import { firstError, useCapabilities, useGatewayState, useOverlays } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { dhcpActionsFor, dhcpEffect, type DhcpActionName } from '../../lib/dhcpForm';
import { dhcpChips } from '../../lib/faults';
import { formatRemaining } from '../../lib/format';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useToastStore } from '../../stores/toasts';
import FaultChip from '../ui/FaultChip.vue';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';
import DhcpActionDialog from './DhcpActionDialog.vue';

const props = defineProps<{
  /** `{device: id}` or `{network: id}`. */
  target: Scope;
  /** The name the card and the toasts use: "ESP32-42", "IoT". */
  name: string;
  /** The network the actions are about has DHCP switched off. */
  dhcpOff?: boolean;
  /** The device is in no network the gateway serves (it was seen only on a network that is no longer configured). */
  noNetwork?: boolean;
  /** The device has no MAC address the DHCP server could know it by. */
  noMac?: boolean;
}>();

const kind = computed(() => (props.target.device ? 'device' : 'network'));
// a gateway of an older build has no DHCP actions: the card is left out, as the step type is (the kinds of the dialog follow the same list)
const capabilities = useCapabilities();
const supported = computed(() => {
  const kinds = capabilities.data.value?.overlay_kinds as string[] | undefined;
  return !kinds || kinds.includes('dhcp');
});
const overlays = useOverlays();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const now = useNow();

/** The actions that are aimed at exactly this device or network. */
const rows = computed<Overlay[]>(() =>
  (overlays.data.value ?? []).filter(
    (o) => o.kind === 'dhcp' && (kind.value === 'device' ? o.target?.device === props.target.device : o.target?.network === props.target.network),
  ),
);
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);
const buttons = computed(() => dhcpActionsFor(kind.value));
/** Why an action cannot be made, when it cannot. */
function unavailable(a: DhcpActionName): string | undefined {
  if (props.noNetwork) return 'This device is in no network with DHCP: an action has nothing to change.';
  if (props.dhcpOff) return 'DHCP is off in this network: an action has nothing to change.';
  if (a === 'force_new_ip' && props.noMac) return 'The device has no MAC address: a reservation cannot be made for it.';
  return undefined;
}
const reason = computed(() => (props.dhcpOff || props.noNetwork ? unavailable('silence') : props.noMac && kind.value === 'device' ? unavailable('force_new_ip') : undefined));

const dialogOpen = ref(false);
const dialogAction = ref<DhcpActionName>('short_lease');
const editing = ref<Overlay>();
function add(a: DhcpActionName) {
  editing.value = undefined;
  dialogAction.value = a;
  dialogOpen.value = true;
}
function edit(o: Overlay) {
  editing.value = o;
  dialogOpen.value = true;
}

const removing = ref<string>();
async function remove(o: Overlay) {
  removing.value = o.id;
  try {
    await deleteOverlay(o.id);
    apiPanel.record('Remove DHCP action', [{ method: 'DELETE', path: `/overlays/${o.id}` }]);
    toasts.push('success', 'DHCP action removed', `${dhcpChips(o.dhcp!).join(', ')} on ${props.name}`);
    await Promise.all(['overlays', 'devices', 'networks', 'faults', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    toasts.push('error', 'The action could not be removed', errorMessage(e));
  } finally {
    removing.value = undefined;
  }
}

const stateLabels: Record<string, string> = { effective: 'effective', overridden: 'overridden', disabled: 'disabled', partially_overridden: 'partially overridden' };
function expiry(o: Overlay): string {
  return o.expires_at ? formatRemaining(new Date(o.expires_at).getTime() - now.value.getTime()) : 'no TTL';
}
</script>

<template>
  <SectionCard v-if="supported" title="DHCP test actions">
    <div class="flex flex-col gap-3" data-testid="dhcp-card">
      <p class="m-0 text-[13px] text-text-2">
        {{
          kind === 'device'
            ? 'Change what the DHCP server does for this device. A client notices at its next renewal: a short lease brings it forward.'
            : 'Change what the DHCP server does for every client of this network. A client notices at its next renewal: a short lease brings it forward.'
        }}
      </p>
      <div class="flex flex-wrap gap-2" role="group" aria-label="DHCP actions">
        <button
          v-for="a in buttons"
          :key="a.value"
          type="button"
          class="btn btn-sm"
          :disabled="blocked || !!unavailable(a.value)"
          :data-testid="`dhcp-add-${a.value}`"
          @click="add(a.value)"
        >
          {{ a.label }}
        </button>
      </div>
      <p v-if="reason" class="m-0 text-[13px] text-text-2" data-testid="dhcp-unavailable">{{ reason }}</p>

      <LoadError v-if="firstError(overlays)" :error="firstError(overlays)" what="The DHCP actions are not loaded" :retry="overlays.refetch" />
      <p v-else-if="overlays.isLoading.value" class="m-0 text-[13px] text-text-2" role="status" data-testid="dhcp-loading">Loading…</p>
      <p v-else-if="rows.length === 0" class="m-0 text-[13px] text-text-3" data-testid="dhcp-none">No action is in force.</p>
      <ul v-else class="m-0 flex list-none flex-col gap-2 p-0" data-testid="dhcp-actions">
        <li v-for="o in rows" :key="o.id" class="flex flex-col gap-1 text-[13px]" data-testid="dhcp-action">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <FaultChip v-for="c in dhcpChips(o.dhcp!)" :key="c" :text="c" />
            <span class="text-xs" :class="o.state === 'effective' ? 'text-text-3' : 'text-fault-text'" data-testid="dhcp-action-state">{{ stateLabels[o.state ?? 'effective'] ?? o.state }}</span>
            <span class="mono text-xs" :class="o.expires_at ? 'text-fault-text' : 'text-text-2'">{{ expiry(o) }}</span>
          </div>
          <div v-if="o.dhcp?.action === 'force_new_ip'" class="text-text-2" data-testid="dhcp-new-ip">
            {{ name }} is moved to <span class="mono">{{ o.dhcp.ip }}</span> when its lease renews; until then it keeps the address it has.
          </div>
          <div v-else-if="o.dhcp?.action === 'delete_lease'" class="text-text-2">The leases were deleted when the action was applied.</div>
          <div v-else class="text-text-2">{{ dhcpEffect(o.dhcp!) }}</div>
          <div v-if="o.state === 'overridden'" class="text-fault-text">Another action of this {{ kind }} wins.</div>
          <div class="flex gap-1.5">
            <button type="button" class="btn btn-sm px-2" :disabled="blocked" :aria-label="`Edit ${dhcpChips(o.dhcp!).join(', ')} on ${name}`" @click="edit(o)">Edit</button>
            <button
              type="button"
              class="btn btn-sm px-2"
              :disabled="blocked || removing === o.id"
              :aria-label="`Remove ${dhcpChips(o.dhcp!).join(', ')} on ${name}`"
              @click="remove(o)"
            >
              Remove
            </button>
          </div>
        </li>
      </ul>
    </div>
    <DhcpActionDialog v-model:open="dialogOpen" :target="target" :action="dialogAction" :overlay="editing" />
  </SectionCard>
</template>
