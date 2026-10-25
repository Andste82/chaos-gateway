<script setup lang="ts">
// The active faults with target, effect, affected packets and expiry (plan §2.17: Overview and
// Device detail). An overlay counts down to its expiry; a configured fault is permanent. On the
// device detail an overlay can be removed: not while the gateway is offline or a revision waits
// for confirmation (plan §2.17: controls that would change more are disabled until it is
// resolved), and only after a question when it acts on a whole network or group.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { deleteOverlay } from '../../api/generated/hooks/overlays/overlays';
import type { Overlay } from '../../api/generated/model';
import type { ActiveFault } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { formatCount, formatRemaining } from '../../lib/format';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useToastStore } from '../../stores/toasts';
import AppIcon from '../ui/AppIcon.vue';
import FaultChip from '../ui/FaultChip.vue';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ items: ActiveFault[]; showTarget?: boolean; removable?: boolean; editable?: boolean; removeDisabled?: boolean }>();
const emit = defineEmits<{ edit: [overlay: Overlay] }>();

const now = useNow();
const queryClient = useQueryClient();
const toasts = useToastStore();
const removing = ref<string>();
const asking = ref<ActiveFault>();
const askOpen = computed({
  get: () => !!asking.value,
  set: (v) => {
    if (!v) asking.value = undefined;
  },
});

function expiry(f: ActiveFault): string {
  if (!f.expiresAt) return f.layer === 'config' ? 'configured' : 'no TTL';
  return formatRemaining(new Date(f.expiresAt).getTime() - now.value.getTime());
}

/**
 * The device's own fault overlays (impairment, MTU, tunnel), DNS faults and TLS cases can be edited where they are (a wider
 * scope's on the faults screen; a DHCP action in the DHCP card beside the lease).
 */
function canEdit(f: ActiveFault): boolean {
  const o = f.overlay;
  return !!props.editable && !f.widerScope && (o?.kind === 'fault' || o?.kind === 'dns' || o?.kind === 'tls');
}

function requestRemove(f: ActiveFault) {
  if (f.widerScope) asking.value = f;
  else void remove(f);
}

async function confirmRemove() {
  const f = asking.value;
  asking.value = undefined;
  if (f) await remove(f);
}

const apiPanel = useApiPanelStore();
async function remove(f: ActiveFault) {
  removing.value = f.id;
  try {
    await deleteOverlay(f.id);
    apiPanel.record('Remove fault', [{ method: 'DELETE', path: `/overlays/${f.id}` }]);
    toasts.push('success', 'Fault removed', `${f.chips.join(', ')} on ${f.target}`);
    await queryClient.invalidateQueries({ queryKey: ['overlays'] });
    await queryClient.invalidateQueries({ queryKey: ['devices'] });
  } catch (e) {
    toasts.push('error', 'The fault could not be removed', errorMessage(e));
  } finally {
    removing.value = undefined;
  }
}

const cols = props.showTarget
  ? 'grid-cols-[minmax(160px,230px)_minmax(0,1fr)_110px_90px_32px]'
  : props.editable
    ? 'grid-cols-[minmax(0,1fr)_110px_90px_80px]'
    : 'grid-cols-[minmax(0,1fr)_110px_90px_32px]';
</script>

<template>
  <div role="table" aria-label="Active faults" class="text-sm">
    <div role="row" class="data-head grid gap-3 px-2.5 pb-2" :class="cols">
      <span v-if="showTarget" role="columnheader">Target</span>
      <span role="columnheader">Effect</span>
      <span role="columnheader">Affected</span>
      <span role="columnheader">Expires</span>
      <span role="columnheader"><span class="sr-only">Actions</span></span>
    </div>
    <div v-for="f in items" :key="f.id" role="row" class="data-row grid items-center gap-3 px-2.5 py-2" :class="cols" data-testid="active-fault">
      <span v-if="showTarget" role="cell" class="min-w-0 truncate">
        <RouterLink v-if="f.link ?? f.device" :to="f.link ?? `/devices/${f.device}`" class="text-text no-underline hover:underline">{{ f.target }}</RouterLink>
        <span v-else>{{ f.target }}</span>
        <span v-if="f.destination" class="ml-1 text-text-3">{{ f.destination }}</span>
      </span>
      <span role="cell" class="flex min-w-0 flex-wrap items-center gap-1.5">
        <span v-if="!showTarget && f.destination" class="mr-1 text-text-2">{{ f.destination }}</span>
        <FaultChip v-for="c in f.chips" :key="c" :text="c" />
        <span class="text-xs text-text-3">{{ f.layer === 'overlay' ? 'overlay' : 'configuration' }}</span>
        <span v-if="f.state === 'overridden' || f.state === 'partially_overridden'" class="text-xs text-text-3">· {{ f.state.replace('_', ' ') }}</span>
      </span>
      <span role="cell" class="mono text-[13px] text-text-2">{{ f.packets !== undefined ? `${formatCount(f.packets)} pkts` : '—' }}</span>
      <span role="cell" class="mono text-[13px]" :class="f.expiresAt ? 'text-fault-text' : 'text-text-2'" data-testid="fault-expiry">{{ expiry(f) }}</span>
      <span role="cell" class="flex justify-end gap-1.5">
        <button v-if="canEdit(f)" type="button" class="btn btn-sm px-2" :aria-label="`Edit ${f.chips.join(', ')}`" :disabled="removeDisabled" @click="emit('edit', f.overlay!)">
          Edit
        </button>
        <button
          v-if="removable && f.layer === 'overlay'"
          type="button"
          class="btn btn-sm px-2"
          :aria-label="`Remove ${f.chips.join(', ')}`"
          :disabled="removeDisabled || removing === f.id"
          @click="requestRemove(f)"
        >
          <AppIcon name="close" />
        </button>
      </span>
    </div>
  </div>
  <ModalDialog
    v-model:open="askOpen"
    title="Remove the fault for everyone?"
    :description="asking ? `It acts on ${asking.widerScope}, not only on this device.` : undefined"
  >
    <p v-if="asking" class="m-0 text-sm">
      Removing {{ asking.chips.join(', ') }} lifts it for every device of {{ asking.widerScope }}.
    </p>
    <template #footer>
      <button type="button" class="btn" @click="asking = undefined">Cancel</button>
      <button type="button" class="btn btn-primary" :disabled="removeDisabled" @click="confirmRemove">Remove for everyone</button>
    </template>
  </ModalDialog>
</template>
