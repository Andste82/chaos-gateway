<script setup lang="ts">
// Remove a probe from the configuration (plan §2.12, §2.17 "Probes", M26a). Like the removal of a device (#99) the dialog first
// lists the objects of the draft that name the probe (access rules, configured faults, groups, port forwards, scenarios): the
// gateway refuses a configuration that names a device it does not have (`unknown_reference`), so they go with the probe in the
// same draft: an access rule, a configured fault and a port forward name one device only and are deleted, a group loses the probe
// as a member. A scenario is a file of its own and is not changed: while one targets the probe, the removal waits. The overlays
// that target the probe go with it when the apply is forced (the preview lists them and asks). The submit puts the deletions into
// the configuration draft and opens the preview-and-apply drawer; until then the list marks the probe and Undo takes it back.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import { useActiveConfig, useGatewayState, useOverlays } from '../../composables/gateway';
import { deviceReferences, referenceEffectText, referenceKind } from '../../lib/devices';
import { useDraftStore, type EditOrigin } from '../../stores/draft';
import LoadError from '../ui/LoadError.vue';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ probe: string }>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const state = useGatewayState();
const active = useActiveConfig();

const error = ref('');
const busy = ref(false);
let origin: EditOrigin | undefined;

watch(open, (o) => {
  if (!o) return;
  error.value = '';
  origin = draft.editOrigin();
});

/** The probe as the draft has it (an edit not applied yet included). */
const config = computed(() => draft.effectiveProbes[props.probe] ?? draft.baseConfig?.probes?.[props.probe]);
const name = computed(() => config.value?.name ?? props.probe);

/** The objects of the draft that name the probe. */
const references = computed(() => {
  if (!draft.baseConfig) return [];
  return deviceReferences(
    { id: props.probe, name: name.value },
    {
      rules: draft.effectiveRules,
      faults: draft.effectiveFaults,
      groups: draft.effectiveGroups,
      forwards: draft.effectiveForwards,
      scenarios: draft.effectiveScenarios,
    },
  );
});
/** The references the removal cannot take along (a scenario): they block it. */
const blockers = computed(() => references.value.filter((r) => r.effect === 'blocks'));
const deleted = computed(() => references.value.filter((r) => r.effect === 'delete').length);
const changed = computed(() => references.value.filter((r) => r.effect === 'member').length);

// the overlays that name the probe: applying the removal orphans them (the gateway refuses it without force)
const overlays = useOverlays();
const overlayCount = computed(() => (overlays.data.value ?? []).filter((o) => o.target?.device?.toLowerCase() === props.probe.toLowerCase()).length);
const base = computed(() => draft.base ?? state.data.value?.active_revision);
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);

async function submit() {
  if (blockers.value.length) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty && !draft.baseConfig) draft.track(await getActiveRevision());
    if (!draft.baseConfig?.probes?.[props.probe]) {
      error.value = `${name.value} is not in the configuration of revision ${draft.base}.`;
      return;
    }
    draft.editFrom(origin, () => draft.removeProbe({ id: props.probe, name: name.value }));
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
    description="Remove the probe from the configuration: its namespace and its address go. The change goes into a new revision."
    width="560px"
  >
    <form class="flex flex-col gap-4" novalidate data-testid="remove-probe-form" @submit.prevent="submit">
      <template v-if="!draft.baseConfig">
        <LoadError v-if="active.error.value" :error="active.error.value" what="The configuration is not loaded" :retry="active.refetch" />
        <p v-else class="m-0 text-sm text-text-2" role="status">Loading the configuration…</p>
      </template>
      <template v-else>
        <div
          v-if="references.length"
          class="rounded-md border p-3 text-sm"
          :class="blockers.length ? 'border-block-border bg-surface-2' : 'border-border bg-surface-2'"
          :role="blockers.length ? 'alert' : undefined"
          data-testid="remove-probe-references"
        >
          <p class="m-0">
            <strong>{{ name }}</strong> is used by {{ references.length === 1 ? 'this object' : `these ${references.length} objects` }} of the configuration:
          </p>
          <ul class="my-2 flex list-none flex-col gap-1 p-0">
            <li v-for="r in references" :key="`${r.kind}/${r.id}`" data-testid="remove-probe-reference">
              {{ referenceKind(r) }}
              <RouterLink v-if="r.to" :to="r.to">{{ r.name }}</RouterLink>
              <strong v-else>{{ r.name }}</strong>
              <span :class="r.effect === 'blocks' ? 'text-block-text' : 'text-text-2'"> · {{ referenceEffectText(r, name) }}</span>
            </li>
          </ul>
          <p v-if="blockers.length" class="m-0 text-text-2" data-testid="remove-probe-blocked">
            The removal waits for {{ blockers.length === 1 ? 'the scenario' : 'these scenarios' }}: a scenario is a file of its own and is not changed with
            the probe, and the gateway refuses a configuration that names a device it does not have. Change
            {{ blockers.length === 1 ? 'its' : 'their' }} target first.
          </p>
          <p v-else class="m-0 text-text-2" data-testid="remove-probe-along">
            They go with the probe in the same revision:
            <template v-if="deleted">{{ deleted === 1 ? 'the object that names' : `the ${deleted} objects that name` }} only {{ name }} {{ deleted === 1 ? 'is' : 'are' }} deleted</template
            ><template v-if="deleted && changed">, </template
            ><template v-if="changed">{{ changed === 1 ? 'a group loses' : `${changed} groups lose` }} it as a member and {{ changed === 1 ? 'keeps' : 'keep' }} the others</template>.
            The preview lists what is deleted and what is changed before anything is applied.
          </p>
        </div>
        <div v-if="!blockers.length" class="rounded-md border border-border bg-surface-2 p-3 text-sm" data-testid="remove-probe-preview">
          <div class="mb-1 text-xs text-text-3">What changes</div>
          Probe <strong>{{ name }}</strong> is removed from the configuration in a new revision based on revision {{ base }}.
          <template v-if="!references.length">No rule, fault, group, port forward or scenario of the configuration names it.</template>
          <span v-if="overlayCount" class="mt-2 block text-block-text" data-testid="remove-probe-overlays">
            {{ overlayCount === 1 ? 'An active overlay (a fault, profile or rule applied at runtime) targets' : `${overlayCount} active overlays (faults, profiles or rules applied at runtime) target` }}
            it: the gateway removes {{ overlayCount === 1 ? 'it' : 'them' }} with the probe. The preview lists {{ overlayCount === 1 ? 'it' : 'them' }} and asks before applying.
          </span>
        </div>
      </template>
      <p v-if="pendingConfirm" class="m-0 text-sm text-fault-text">Another revision waits for confirmation; remove the probe after it is resolved.</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || pendingConfirm || !draft.baseConfig || blockers.length > 0">
          {{ references.length ? 'Remove the probe and these references' : 'Preview & apply' }}
        </button>
      </div>
    </form>
  </ModalDialog>
</template>
