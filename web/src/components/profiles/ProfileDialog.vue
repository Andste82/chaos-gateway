<script setup lang="ts">
// The profile editor (plan §2.9, §2.17 "Profiles"; P3-M13-03): a custom profile's name, description
// and parts — an impairment part with the fault dialog's impairment fields (direction, presets,
// latency, jitter, loss and the advanced parameters) and an MTU part (size and mode). A custom
// profile is an object of the configuration: "Add to the changes" puts it into the draft, and the
// unapplied-changes bar, the preview and the apply take it to the gateway, like a configured fault.
// A DNS or TLS part the profile has (defined over the API) is kept. What the gateway refused in the
// last candidate stands under the fields its pointer names. The request the preview would send is
// shown under `</> API`.
import { computed, ref, useId, watch } from 'vue';
import type { Profile } from '../../api/generated/model';
import { mtuModes } from '../../lib/faultForm';
import { newId } from '../../lib/ids';
import { emptyProfileForm, formToProfile, keptParts, profileFieldOf, profileFormErrors, profileToForm, type ProfileField, type ProfileForm } from '../../lib/profileForm';
import { profileChips } from '../../lib/profiles';
import { httpOf, useApiPanelStore } from '../../stores/apiPanel';
import { useDraftStore } from '../../stores/draft';
import ImpairmentFields from '../faults/ImpairmentFields.vue';
import FaultChip from '../ui/FaultChip.vue';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{
  /** The custom profile to edit; a new one without. */
  profile?: { id: string; config: Profile };
  /** The names of all profiles (built-in and custom), for the check that a name is unique. */
  names: string[];
}>();
const open = defineModel<boolean>('open', { required: true });

const draft = useDraftStore();
const apiPanel = useApiPanelStore();
const uid = useId();

const form = ref<ProfileForm>(emptyProfileForm());
const serverErrors = ref<Partial<Record<ProfileField, string>>>({});
const general = ref<string[]>([]);
let refusedForm = '';

const title = computed(() => (props.profile ? 'Edit profile' : 'New profile'));
const id = computed(() => props.profile?.id ?? '<new uuid>');

watch(open, (o) => {
  if (!o) {
    apiPanel.draft = undefined;
    return;
  }
  form.value = props.profile ? profileToForm(props.profile.config) : emptyProfileForm();
  serverErrors.value = {};
  general.value = [];
  // what the gateway refused in the last candidate, until the profile is edited
  const refused = props.profile ? draft.serverErrors[props.profile.id] : undefined;
  if (refused) {
    const each = form.value.impairment.direction === 'each';
    for (const [rest, message] of Object.entries(refused)) {
      const f = profileFieldOf(rest, each);
      if (f) serverErrors.value[f] = message;
      else general.value.push(message);
    }
    refusedForm = JSON.stringify(form.value);
  }
});
watch(
  form,
  (f) => {
    if (refusedForm && JSON.stringify(f) !== refusedForm) {
      serverErrors.value = {};
      general.value = [];
      refusedForm = '';
    }
  },
  { deep: true },
);

const kept = computed(() => keptParts(props.profile?.config));
const others = computed(() => props.names.filter((n) => n.toLowerCase() !== props.profile?.config.name.toLowerCase()));
const clientErrors = computed(() => profileFormErrors(form.value, others.value, props.profile?.config));
const errors = computed<Partial<Record<string, string>>>(() => ({ ...serverErrors.value, ...clientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const result = computed(() => formToProfile(form.value, props.profile?.config));
const chips = computed(() => profileChips(result.value.parts));

// what the preview would send, for the `</> API` panel
watch(
  [open, result],
  () => {
    if (!open.value) return;
    apiPanel.draft = {
      title: `${title.value} (configuration)`,
      calls: [
        {
          method: 'POST',
          path: '/revisions',
          headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base}"` },
          body: { profiles: { [id.value]: result.value } },
        },
      ],
    };
  },
  { deep: true, immediate: true },
);

function submit() {
  if (!valid.value) return;
  draft.setProfile(props.profile?.id ?? newId(), result.value);
  open.value = false;
}

const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
const mtuHint = computed(() => mtuModes.find((m) => m.value === form.value.mtuMode)?.hint);
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" width="680px">
    <form class="flex flex-col gap-4" novalidate data-testid="profile-dialog" @submit.prevent="submit">
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="label" :for="fid('name')">Name</label>
          <input
            :id="fid('name')"
            v-model="form.name"
            class="input mono"
            placeholder="e.g. flaky-lte"
            :aria-invalid="!!errors.name"
            :aria-describedby="errors.name ? eid('name') : undefined"
          />
          <p v-if="errors.name" :id="eid('name')" class="field-error">{{ errors.name }}</p>
        </div>
        <div class="col-span-2">
          <label class="label" :for="fid('description')">Description</label>
          <input
            :id="fid('description')"
            v-model="form.description"
            class="input"
            placeholder="optional"
            :aria-invalid="!!errors.description"
            :aria-describedby="errors.description ? eid('description') : undefined"
          />
          <p v-if="errors.description" :id="eid('description')" class="field-error">{{ errors.description }}</p>
        </div>
      </div>

      <fieldset class="m-0 flex flex-col gap-3 rounded-md border border-border p-3">
        <legend class="px-1 text-[13px]">
          <label class="flex items-center gap-2">
            <input v-model="form.impairmentOn" type="checkbox" />
            Impairment part
          </label>
        </legend>
        <ImpairmentFields v-if="form.impairmentOn" v-model="form.impairment" :errors="errors" />
        <p v-else class="m-0 text-[13px] text-text-3">No impairment: the traffic keeps its delay, loss and rate.</p>
      </fieldset>

      <fieldset class="m-0 flex flex-col gap-3 rounded-md border border-border p-3">
        <legend class="px-1 text-[13px]">
          <label class="flex items-center gap-2">
            <input v-model="form.mtuOn" type="checkbox" />
            MTU part
          </label>
        </legend>
        <template v-if="form.mtuOn">
          <div class="grid grid-cols-3 gap-3">
            <div>
              <label class="label" :for="fid('mtuSize')">Packet size (bytes)</label>
              <input
                :id="fid('mtuSize')"
                v-model="form.mtuSize"
                class="input mono"
                inputmode="numeric"
                :aria-invalid="!!errors.mtuSize"
                :aria-describedby="errors.mtuSize ? eid('mtuSize') : undefined"
              />
              <p v-if="errors.mtuSize" :id="eid('mtuSize')" class="field-error">{{ errors.mtuSize }}</p>
            </div>
            <div class="col-span-2">
              <label class="label" :for="fid('mtuMode')">Mode</label>
              <select
                :id="fid('mtuMode')"
                v-model="form.mtuMode"
                class="input"
                :aria-invalid="!!errors.mtuMode"
                :aria-describedby="errors.mtuMode ? eid('mtuMode') : undefined"
              >
                <option v-for="m in mtuModes" :key="m.value" :value="m.value">{{ m.label }}</option>
              </select>
              <p v-if="errors.mtuMode" :id="eid('mtuMode')" class="field-error">{{ errors.mtuMode }}</p>
            </div>
          </div>
          <p class="m-0 text-[13px] text-text-2">{{ mtuHint }}</p>
        </template>
        <p v-else class="m-0 text-[13px] text-text-3">No MTU limit.</p>
      </fieldset>

      <p v-if="kept.dns || kept.tls" class="m-0 text-[13px] text-text-2" data-testid="profile-kept">
        Its {{ [kept.dns ? 'DNS' : '', kept.tls ? 'TLS' : ''].filter(Boolean).join(' and ') }} part is kept as it is; it is defined over the API.
      </p>
      <p v-if="errors.parts" class="field-error m-0" role="alert">{{ errors.parts }}</p>

      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" aria-live="polite" data-testid="profile-preview">
        <div class="mb-1 text-xs text-text-3">Preview</div>
        <span class="flex flex-wrap gap-1.5">
          <FaultChip v-for="c in chips" :key="c" :text="c" />
          <span v-if="chips.length === 0" class="text-text-3">no parameters yet</span>
        </span>
        <div class="mt-1.5 text-[13px] text-text-2">Goes into the configuration after preview and apply; then it can be activated on a scope.</div>
      </div>

      <details v-if="apiPanel.draft" class="text-[13px]" data-testid="profile-api">
        <summary class="cursor-pointer text-text-2">&lt;/&gt; API · what the preview sends</summary>
        <pre
          v-for="(c, i) in apiPanel.draft.calls"
          :key="i"
          class="mono m-0 mt-2 max-h-48 overflow-auto rounded-md border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]"
          data-testid="api-call"
          >{{ httpOf(c) }}</pre
        >
      </details>

      <p v-for="g in general" :key="g" class="field-error m-0" role="alert">{{ g }}</p>

      <div class="flex justify-end gap-2">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="!valid">{{ profile ? 'Save to the changes' : 'Add to the changes' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
