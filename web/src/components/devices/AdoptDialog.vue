<script setup lang="ts">
// Adopt a discovered device (plan §2.3, §2.17): it becomes a configured device under the same UUID,
// with a name and its MAC (or addresses) and its network. The dialog asks for the name and says in
// words what will change; "Preview & apply" puts the device into the configuration draft and opens
// the preview-and-apply drawer, like every configuration change: the dry run, then the apply with
// the verified toast. A configuration changed by someone else meanwhile leads to the
// concurrent-change dialog (reload and reapply). Backing out of the drawer keeps the adoption in the
// unapplied-changes bar.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { DeviceView } from '../../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../../api/generated/zod/system/system';
import { useGatewayState } from '../../composables/gateway';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ device: DeviceView | undefined; networkName?: string }>();
const open = defineModel<boolean>('open', { required: true });

const state = useGatewayState();
const draft = useDraftStore();

const name = ref('');
const error = ref('');
const busy = ref(false);

const macs = computed(() => props.device?.observed.macs ?? []);
const ips = computed(() => props.device?.observed.addresses ?? []);
const base = computed(() => draft.base ?? state.data.value?.active_revision);
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);

watch(open, (o) => {
  if (!o || !props.device) return;
  const hostname = props.device.observed.lease?.hostname;
  name.value = hostname && nameRegExp.test(hostname) ? hostname : props.device.name;
  error.value = '';
});

const nameError = computed(() =>
  nameRegExp.test(name.value) ? '' : 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.',
);

async function adopt() {
  const d = props.device;
  if (!d || nameError.value) return;
  busy.value = true;
  error.value = '';
  try {
    // the draft's base: the active revision now, unless the draft already has edits on another one
    if (!draft.dirty) draft.track(await getActiveRevision());
    const identifiers = macs.value.length ? { macs: macs.value } : { ipv4: ips.value };
    draft.setDevice(d.id, { name: name.value, identifiers, ...(d.network ? { network: d.network } : {}) });
    open.value = false;
    draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog v-model:open="open" title="Adopt device" description="Add the discovered device to the configuration, so rules and faults can name it.">
    <form class="flex flex-col gap-4" novalidate @submit.prevent="adopt">
      <div>
        <label class="label" for="adopt-name">Name</label>
        <input id="adopt-name" v-model="name" class="input" :aria-invalid="!!nameError" aria-describedby="adopt-name-error" />
        <p v-if="nameError" id="adopt-name-error" class="field-error">{{ nameError }}</p>
      </div>
      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="adopt-preview">
        <div class="mb-1 text-xs text-text-3">What changes</div>
        Device <strong>{{ name || '…' }}</strong> is added
        <template v-if="macs.length">with MAC <span class="mono">{{ macs.join(', ') }}</span></template>
        <template v-else>with address <span class="mono">{{ ips.join(', ') }}</span></template>
        <template v-if="networkName"> in network {{ networkName }}</template> in a new revision based on revision {{ base }}. Traffic is not changed.
      </div>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; adopt after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || !!nameError || pendingConfirm">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
