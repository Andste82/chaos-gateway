<script setup lang="ts">
// The settings of the managed BIRD instance (plan §2.2.2; P3-M14-01): the router id, the local AS for
// BGP, and the external mode, which imports the routes another daemon writes into a kernel table with the
// same import filter. Each check stands under its field; the change goes into the configuration draft and
// the preview-and-apply drawer opens. Settings the gateway refused open again with its messages under
// their fields, until the form is changed.
import { computed, ref, watch } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import { useGatewayState } from '../../composables/gateway';
import { createMergePatch } from '../../lib/mergePatch';
import {
  formFromRouting,
  routingFromForm,
  routingSettingsErrors,
  routingSettingsFieldOf,
  type RoutingSettingsField,
  type RoutingSettingsForm,
} from '../../lib/routingForm';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore } from '../../stores/draft';
import ModalDialog from '../ui/ModalDialog.vue';
import TextField from '../ui/TextField.vue';

const open = defineModel<boolean>('open', { required: true });
const draft = useDraftStore();
const state = useGatewayState();
const apiPanel = useApiPanelStore();

const form = ref<RoutingSettingsForm>(formFromRouting(undefined));
const tried = ref(false);
const error = ref('');
const busy = ref(false);
const refused = ref<Record<string, string>>({});
let refusedForm = '';

watch(open, (o) => {
  if (!o) return;
  form.value = formFromRouting(draft.effectiveRouting);
  refused.value = draft.serverErrors['routing'] ?? {};
  refusedForm = JSON.stringify(form.value);
  tried.value = Object.keys(refused.value).length > 0;
  error.value = '';
});

const bgp = computed(() => Object.values(draft.effectiveRouting.protocols ?? {}).some((p) => p.type === 'bgp'));
const formErrors = computed(() => routingSettingsErrors(form.value, bgp.value));
const server = computed(() => {
  const byField: Partial<Record<RoutingSettingsField, string>> = {};
  const general: string[] = [];
  if (JSON.stringify(form.value) !== refusedForm) return { byField, general };
  for (const [pointer, message] of Object.entries(refused.value)) {
    const f = routingSettingsFieldOf(pointer);
    if (f) byField[f] = message;
    else general.push(message);
  }
  return { byField, general };
});
const errors = computed(() => ({ ...server.value.byField, ...formErrors.value }));
const valid = computed(() => Object.keys(formErrors.value).length === 0 && Object.keys(server.value.byField).length + server.value.general.length === 0);
const shown = (f: RoutingSettingsField) => (tried.value ? errors.value[f] : undefined);

watch(
  [open, form],
  ([o]) => {
    apiPanel.draft = o
      ? {
          title: 'Routing settings',
          calls: [
            {
              method: 'POST',
              path: '/revisions',
              headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base ?? state.data.value?.active_revision}"` },
              body: { routing: createMergePatch(draft.baseRouting, routingFromForm(form.value, draft.effectiveRouting)) ?? {} },
              note: 'the candidate; then …/preview and …/apply',
            },
          ],
        }
      : undefined;
  },
  { deep: true },
);

async function submit() {
  tried.value = true;
  if (!valid.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (!draft.dirty) draft.track(await getActiveRevision());
    draft.setRouting(routingFromForm(form.value, draft.effectiveRouting));
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
  <ModalDialog v-model:open="open" title="Routing settings" description="The managed BIRD instance: its router id, the local AS and the external mode." width="600px">
    <form class="flex flex-col gap-4" novalidate data-testid="routing-settings-form" @submit.prevent="submit">
      <div class="grid grid-cols-2 gap-4">
        <TextField v-model="form.routerId" label="Router id" mono placeholder="10.10.0.1" :error="shown('routerId')" hint="Empty: the lowest gateway address of the local networks." />
        <TextField v-model="form.asn" label="Local AS" mono placeholder="65001" :error="shown('asn')" hint="For BGP; private: 64512 to 65534." />
      </div>
      <fieldset class="m-0 flex flex-col gap-3 border-0 p-0">
        <legend class="label">External mode</legend>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.externalEnabled" type="checkbox" /> Import the routes another daemon writes into a kernel table</label>
        <template v-if="form.externalEnabled">
          <TextField v-model="form.externalTable" label="Kernel table" mono placeholder="200" :error="shown('externalTable')" />
          <TextField
            v-model="form.externalPrefixes"
            label="Allowed prefixes (one per line; le 24 also takes longer ones up to /24)"
            mono
            :rows="2"
            :error="shown('externalPrefixes')"
            hint="Empty: any prefix that is not forbidden."
          />
          <div class="grid grid-cols-2 gap-4">
            <TextField v-model="form.externalMaxPrefixes" label="Most prefixes taken" mono placeholder="100" :error="shown('externalMaxPrefixes')" />
            <label class="mt-6 flex items-center gap-2 text-sm"><input v-model="form.externalAllowDefault" type="checkbox" /> Accept the default route</label>
          </div>
        </template>
      </fieldset>
      <p v-if="server.general.length" class="field-error" role="alert">The gateway refused the settings: {{ server.general.join(' ') }}</p>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || (tried && !valid)">Preview &amp; apply</button>
      </div>
    </form>
  </ModalDialog>
</template>
