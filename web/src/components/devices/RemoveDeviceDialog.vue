<script setup lang="ts">
// Remove a configured device from the configuration (plan §2.3, §2.17 "Devices"; #78). The dialog first
// lists the configuration objects that name the device as the draft has them (access rules, configured
// faults, groups, port forwards, scenarios): the gateway refuses a configuration that names a device it
// does not have (`unknown_reference`), so the removal waits until they no longer name it. Otherwise it
// says what goes with the device: its fixed IP (the DHCP reservation) and the overlays that target it,
// which the gateway removes with it only when the apply is forced (the preview lists them and asks). A
// device adopted from discovery comes back as a discovered one under the same id when the gateway sees
// it with the MAC that id was derived from (idMac); one configured with an id of its own gets a new one.
// "Preview & apply" puts the deletion into the configuration draft and opens the preview-and-apply
// drawer; until it is applied, the device list marks it and Undo takes it back.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import type { Device, DeviceView } from '../../api/generated/model';
import { useActiveConfig, useGatewayState, useOverlays } from '../../composables/gateway';
import { deviceReferences, idMac, referenceKind } from '../../lib/devices';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import LoadError from '../ui/LoadError.vue';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ device: DeviceView }>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
// the draft's base: the references are looked up in the configuration as the draft has it
const active = useActiveConfig();

const error = ref('');
const busy = ref(false);
let origin: EditOrigin | undefined;

watch(open, (o) => {
  if (!o) return;
  error.value = '';
  origin = draft.editOrigin();
});

/** The device as the draft has it (an edit not applied yet included). */
const config = computed<Device | undefined>(() => {
  const id = props.device.id;
  const edited = draft.devices[id];
  if (edited === null) return undefined;
  return edited ?? draft.baseConfig?.devices?.[id] ?? props.device.config;
});
const name = computed(() => config.value?.name ?? props.device.name);
const references = computed(() =>
  draft.baseConfig
    ? deviceReferences(
        { id: props.device.id, name: name.value },
        {
          rules: draft.effectiveRules,
          faults: draft.effectiveFaults,
          groups: draft.baseConfig.groups,
          forwards: draft.effectiveForwards,
          scenarios: draft.effectiveScenarios,
        },
      )
    : [],
);
/** The MAC the device's id was derived from, when it was adopted from discovery. */
const sameIdMac = computed(() => idMac(props.device.id, config.value?.identifiers?.macs ?? props.device.observed.macs));
// the overlays that name the device: applying the removal orphans them (the gateway refuses it without force)
const overlays = useOverlays();
const overlayCount = computed(() => (overlays.data.value ?? []).filter((o) => o.target?.device?.toLowerCase() === props.device.id.toLowerCase()).length);
const base = computed(() => draft.base ?? state.data.value?.active_revision);
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);

async function submit() {
  if (references.value.length) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty && !draft.baseConfig) draft.track(await getActiveRevision());
    if (!draft.baseConfig?.devices?.[props.device.id]) {
      error.value = `${name.value} is not in the configuration of revision ${draft.base}.`;
      return;
    }
    draft.editFrom(origin, () => draft.setDevice(props.device.id, null));
    open.value = false;
    if (draft.dirty) draft.drawerOpen = true;
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog
    v-model:open="open"
    :title="`Remove ${name}`"
    description="Remove the device from the configuration, so it is a discovered device again. The change goes into a new revision."
    width="560px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="remove-device-form" @submit.prevent="submit">
      <template v-if="!draft.baseConfig">
        <LoadError v-if="active.error.value" :error="active.error.value" what="The configuration is not loaded" :retry="active.refetch" />
        <p v-else class="m-0 text-sm text-text-2" role="status">Loading the configuration…</p>
      </template>
      <div v-else-if="references.length" class="rounded-md border border-block-border bg-surface-2 p-3 text-sm" role="alert" data-testid="remove-device-references">
        <p class="m-0">
          <strong>{{ name }}</strong> is used by {{ references.length === 1 ? 'this object' : `these ${references.length} objects` }} of the configuration:
        </p>
        <ul class="my-2 flex list-none flex-col gap-1 p-0">
          <li v-for="r in references" :key="`${r.kind}/${r.id}`" data-testid="remove-device-reference">
            {{ referenceKind(r) }}
            <RouterLink v-if="r.to" :to="r.to">{{ r.name }}</RouterLink>
            <strong v-else>{{ r.name }}</strong>
          </li>
        </ul>
        <p class="m-0 text-text-2">
          Change or remove them first: the gateway refuses a configuration that names a device it does not have.
          <template v-if="references.some((r) => r.kind === 'group')"> A group's members are changed in the configuration (YAML or the API).</template>
        </p>
      </div>
      <div v-else class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="remove-device-preview">
        <div class="mb-1 text-xs text-text-3">What changes</div>
        Device <strong>{{ name }}</strong> is removed from the configuration in a new revision based on revision {{ base }}.
        <template v-if="config?.fixed_ip">
          Its fixed IP <span class="mono">{{ config.fixed_ip }}</span> (the DHCP reservation) goes with it.
        </template>
        No rule, fault, group, port forward or scenario of the configuration names it.
        <span v-if="overlayCount" class="mt-2 block text-block-text" data-testid="remove-device-overlays">
          {{ overlayCount === 1 ? 'An active overlay (a fault, profile or rule applied at runtime) targets' : `${overlayCount} active overlays (faults, profiles or rules applied at runtime) target` }}
          it: the gateway removes {{ overlayCount === 1 ? 'it' : 'them' }} with the device. The preview lists {{ overlayCount === 1 ? 'it' : 'them' }} and asks before applying.
        </span>
        <span class="mt-2 block text-text-2" data-testid="remove-device-comes-back">
          <template v-if="sameIdMac">
            When the gateway sees it again with the MAC <span class="mono">{{ sameIdMac }}</span> its id was derived from, it is listed as a
            discovered device under the same id.
          </template>
          <template v-else>
            Its id was not derived from one of its MACs (it was configured with an id of its own), so when the gateway sees it again it is
            listed as a discovered device under a new id.
          </template>
        </span>
      </div>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; remove the device after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || pendingConfirm || !draft.baseConfig || references.length > 0">
          Preview &amp; apply
        </button>
      </div>
    </form>
  </ModalDialog>
</template>
