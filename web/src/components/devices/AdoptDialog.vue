<script setup lang="ts">
// Adopt a discovered device (plan §2.3, §2.17): it becomes a configured device under the same UUID,
// with a name and its MAC, through a candidate revision that is applied and verified. The dialog
// says what will change before anything does. A configuration changed by someone else meanwhile is
// refused by the gateway (409 revision_conflict); the full conflict dialog comes with M13.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { applyRevision, createRevision, discardRevision } from '../../api/generated/hooks/revisions/revisions';
import type { DeviceView } from '../../api/generated/model';
import { completeSetupBodyConfigurationOneNetworksOneNameRegExp as nameRegExp } from '../../api/generated/zod/system/system';
import { useGatewayState } from '../../composables/gateway';
import { useToastStore } from '../../stores/toasts';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ device: DeviceView | undefined; networkName?: string }>();
const open = defineModel<boolean>('open', { required: true });

const state = useGatewayState();
const queryClient = useQueryClient();
const toasts = useToastStore();

const name = ref('');
const error = ref('');
const busy = ref(false);

const macs = computed(() => props.device?.observed.macs ?? []);
const ips = computed(() => props.device?.observed.addresses ?? []);
const base = computed(() => state.data.value?.active_revision);
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
  if (!d || nameError.value || base.value === undefined) return;
  busy.value = true;
  error.value = '';
  let candidate: number | undefined;
  try {
    const identifiers = macs.value.length ? { macs: macs.value } : { ipv4: ips.value };
    const patch = { devices: { [d.id]: { name: name.value, identifiers, ...(d.network ? { network: d.network } : {}) } } };
    const rev = await createRevision(
      patch,
      { message: `adopt ${name.value}` },
      { headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${base.value}"` } },
    );
    candidate = rev.id;
    const res = await applyRevision(rev.id);
    candidate = undefined;
    toasts.push(
      'success',
      `Revision ${res.revision} applied and verified${res.duration_ms !== undefined ? ` in ${res.duration_ms} ms` : ''}`,
      `${name.value} is a configured device now.`,
    );
    open.value = false;
    await queryClient.invalidateQueries({ queryKey: ['devices'] });
    await queryClient.invalidateQueries({ queryKey: ['state'] });
  } catch (e) {
    if (e instanceof ApiError && e.code === 'revision_conflict') {
      error.value = 'The configuration was changed meanwhile. Close the dialog and adopt the device again.';
      await queryClient.invalidateQueries({ queryKey: ['state'] });
    } else if (e instanceof ApiError && e.code === 'name_taken') {
      error.value = 'Another device has this name.';
    } else {
      error.value = errorMessage(e);
    }
    // a candidate the apply refused is of no use
    if (candidate !== undefined) await discardRevision(candidate).catch(() => undefined);
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
        <button type="submit" class="btn btn-primary" :disabled="busy || !!nameError || pendingConfirm">
          {{ busy ? 'Applying…' : 'Adopt and apply' }}
        </button>
      </div>
    </form>
  </ModalDialog>
</template>
