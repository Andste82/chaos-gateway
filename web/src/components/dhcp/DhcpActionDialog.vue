<script setup lang="ts">
// The DHCP test action dialog (plan §2.7, §2.17 "Device detail" and "Networks", M23b): one of the five actions (short lease,
// delete lease, force a new IP, set options, silence) for a device or a network, with what the action needs and the duration
// of the overlay, a live preview sentence and the request under `</> API`. The device detail and the network card open it
// with the action their button names (a network has no "Force a new IP"); the faults screen opens it to edit an action.
// An action is an overlay written at once (`POST /overlays`, the answer comes after Kea took the configuration) and appears
// with its TTL. The key of an action is its action, so an edit keeps it (the action cannot be changed); an edit of an action of
// another owner (a token, a run) is written as the admin's and replaces the old one. A forced new IP shows the address the
// device is moved to: the one the gateway chose, which only the overlay as read has. Each message stands under its field and
// Apply stays disabled until the form is valid; the gateway's refusal (422: no DHCP in the network, a device without a MAC, an
// address outside the network, a configuration Kea does not take) is put under the field it names.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { createOverlay, deleteOverlay } from '../../api/generated/hooks/overlays/overlays';
import type { Overlay, OverlayRequest, Scope } from '../../api/generated/model';
import { useNow } from '../../composables/useNow';
import { useConnectionStore } from '../../stores/connection';
import { useGatewayState, useNames } from '../../composables/gateway';
import { gatewayNow } from '../../lib/clock';
import { dhcpBody, dhcpEffect, dhcpErrors, dhcpFieldOf, dhcpScopeOf, dhcpToForm, emptyDhcp, type DhcpActionName, type DhcpField, type DhcpForm } from '../../lib/dhcpForm';
import { durationChoices, keepError, lifetimeOf, overlayLifetime, ttlText, durations, untilText, type Lifetime } from '../../lib/faultForm';
import { describeOwner, describeScope } from '../../lib/faults';
import { formatHM } from '../../lib/format';
import { httpOf, useApiPanelStore, type ApiCall } from '../../stores/apiPanel';
import { useToastStore } from '../../stores/toasts';
import DisclosureButton from '../ui/DisclosureButton.vue';
import ModalDialog from '../ui/ModalDialog.vue';
import DhcpFields from './DhcpFields.vue';

const props = defineProps<{
  /** What the action is aimed at: a device or a network. */
  target: Scope;
  /** The action a new one starts with (the button that opened the dialog). */
  action?: DhcpActionName;
  /** The overlay to edit. */
  overlay?: Overlay;
}>();
const open = defineModel<boolean>('open', { required: true });

const { names } = useNames();
const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const uid = useId();
const apiOpen = ref(false);

const form = ref<DhcpForm>(emptyDhcp());
const life = ref<Lifetime>({ duration: '15m', keepUntil: '', keepTtl: '' });
const serverErrors = ref<Partial<Record<DhcpField | 'duration', string>>>({});
const error = ref('');
const busy = ref(false);

const editing = computed(() => !!props.overlay);
const scope = computed(() => dhcpScopeOf(props.overlay?.target ?? props.target));
const targetName = computed(() => describeScope(props.overlay?.target ?? props.target, names.value));
const title = computed(() => (editing.value ? 'Edit DHCP action' : 'DHCP action'));
const owner = computed(() => (props.overlay && props.overlay.owner.type !== 'user' ? describeOwner(props.overlay.owner) : undefined));

watch(open, (o) => {
  if (!o) {
    apiPanel.draft = undefined;
    return;
  }
  error.value = '';
  serverErrors.value = {};
  if (props.overlay) {
    form.value = dhcpToForm(props.overlay.dhcp);
    life.value = overlayLifetime(props.overlay);
  } else {
    form.value = emptyDhcp(props.action ?? 'short_lease');
    life.value = { duration: '15m', keepUntil: '', keepTtl: '' };
  }
});

const now = useNow();
const clientErrors = computed<Partial<Record<DhcpField | 'duration', string>>>(() => {
  const e = dhcpErrors(form.value);
  const expired = keepError(life.value, now.value);
  return expired ? { ...e, duration: expired } : e;
});
const errors = computed(() => ({ ...serverErrors.value, ...clientErrors.value }));
const valid = computed(() => Object.keys(clientErrors.value).length === 0);
const durationOptions = computed(() => durationChoices(life.value, props.overlay?.ttl, now.value));
const request = computed<OverlayRequest>(() => ({ target: props.overlay?.target ?? props.target, dhcp: dhcpBody(form.value), ...lifetimeOf(life.value, now.value) }));
const preview = computed(() => `${targetName.value} · DHCP → ${dhcpEffect(dhcpBody(form.value))} · ${untilText(life.value)}`);

watch(
  [open, request],
  () => {
    if (!open.value) return;
    apiPanel.draft = { title: title.value, calls: [{ method: 'POST', path: '/overlays', body: request.value }] };
  },
  { deep: true, immediate: true },
);

/** The form as it was when the gateway refused it; an edit after that takes the messages away. */
let refusedForm = '';
watch(
  [form, life],
  () => {
    if (JSON.stringify([form.value, life.value]) !== refusedForm) serverErrors.value = {};
  },
  { deep: true },
);

/** The gateway's problems under the fields they name; the others below the form. */
function showRefusal(problems: { path?: string; message?: string }[]) {
  const out: Partial<Record<DhcpField | 'duration', string>> = {};
  const rest: string[] = [];
  for (const x of problems) {
    const p = x.path ?? '';
    const f = p.startsWith('/dhcp') ? dhcpFieldOf(p.slice('/dhcp'.length)) : /^\/(ttl|expires_in)/.test(p) ? 'duration' : undefined;
    if (f) out[f] = x.message ?? 'not valid';
    else rest.push(x.message ?? '');
  }
  serverErrors.value = out;
  refusedForm = JSON.stringify([form.value, life.value]);
  error.value = rest.length ? rest.join(' ') : 'The gateway refused the action; see the marked fields.';
}

async function submit() {
  if (!valid.value || busy.value) return;
  error.value = '';
  busy.value = true;
  const req: OverlayRequest = { target: props.overlay?.target ?? props.target, dhcp: dhcpBody(form.value), ...lifetimeOf(life.value, gatewayNow()) };
  try {
    const ov = await createOverlay(req);
    const calls: ApiCall[] = [{ method: 'POST', path: '/overlays', body: req }];
    const old = props.overlay;
    // the owner is part of the key: an action of a token or a run that is edited is replaced by the admin's
    let leftOver = '';
    if (old && old.id !== ov.id) {
      calls.push({ method: 'DELETE', path: `/overlays/${old.id}`, note: owner.value ? `the action of ${owner.value} before the edit` : 'the action before the edit' });
      await deleteOverlay(old.id).catch((e: unknown) => {
        if (!(e instanceof ApiError && e.status === 404)) leftOver = errorMessage(e);
      });
    }
    apiPanel.record(editing.value ? 'Edit DHCP action' : 'DHCP action', calls);
    const kept = life.value.duration === 'keep' && life.value.keepUntil;
    const ends = kept ? `Expires at ${formatHM(life.value.keepUntil)} as before.` : (req.ttl ? `Expires after ${durations.find((d) => d.value === life.value.duration)?.label ?? ttlText(req.ttl)}.` : 'Until removed.');
    if (leftOver) {
      toasts.push('error', 'The action before the edit is still active', `The edited action is active on ${targetName.value}, but removing the old one failed (${leftOver}). Remove it on the faults screen.`);
    } else if (ov.dhcp?.action === 'force_new_ip') {
      // the address the gateway chose is only in the overlay as it reads back
      toasts.push('success', `${targetName.value} is moved to ${ov.dhcp.ip ?? 'a new address'}`, `The device is refused at its next renewal and asks for it. ${ends}`);
    } else {
      toasts.push('success', `DHCP action active on ${targetName.value}`, ov.dhcp?.action === 'delete_lease' ? `The leases are deleted. ${ends}` : ends);
    }
    open.value = false;
    await Promise.all(['overlays', 'devices', 'networks', 'faults', 'state'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.problem?.errors?.length) showRefusal(e.problem.errors);
    else error.value = errorMessage(e);
  } finally {
    busy.value = false;
  }
}

const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);
const fieldId = (f: string) => `${uid}-${f}`;
</script>

<template>
  <ModalDialog v-model:open="open" :title="title" width="600px">
    <form class="flex flex-col gap-4" novalidate data-testid="dhcp-dialog" @submit.prevent="submit">
      <p class="m-0 text-sm" data-testid="dhcp-target">
        For <span class="font-semibold">{{ targetName }}</span> <span class="text-text-2">· {{ scope === 'device' ? 'the device' : 'every client of the network' }}</span>
      </p>

      <DhcpFields v-model="form" :errors="errors" :scope="scope" :locked="editing" />

      <div class="max-w-[260px]">
        <label class="label" :for="fieldId('duration')">Duration</label>
        <select
          :id="fieldId('duration')"
          v-model="life.duration"
          class="input"
          :aria-invalid="!!errors.duration"
          :aria-describedby="errors.duration ? `${fieldId('duration')}-error` : undefined"
        >
          <option v-for="d in durationOptions" :key="d.value" :value="d.value">{{ d.label }}</option>
        </select>
        <p v-if="errors.duration" :id="`${fieldId('duration')}-error`" class="field-error">{{ errors.duration }}</p>
        <p v-else class="m-0 mt-1 text-[13px] text-text-2">
          {{ form.action === 'delete_lease' ? 'The leases are deleted once, now; the action stays in the list for this long.' : 'The action is taken away after this time. When it goes, the DHCP server is as configured again.' }}
        </p>
      </div>

      <p v-if="owner" class="m-0 text-[13px] text-fault-text" data-testid="dhcp-owner">Set by {{ owner }}. Applying removes it and puts an action of yours in its place.</p>

      <div class="rounded-md border border-border bg-surface-2 p-3 text-sm" aria-live="polite" data-testid="dhcp-preview">
        <div class="mb-1 text-xs text-text-3">Preview</div>
        {{ preview }}
      </div>

      <div v-if="apiPanel.draft" class="flex flex-col" data-testid="dhcp-api">
        <DisclosureButton v-model:open="apiOpen" :controls="`${uid}-api`">&lt;/&gt; API · what Apply sends</DisclosureButton>
        <div v-if="apiOpen" :id="`${uid}-api`">
          <pre
            v-for="(c, i) in apiPanel.draft.calls"
            :key="i"
            class="mono m-0 mt-2 max-h-48 overflow-auto rounded-md border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]"
            data-testid="api-call"
            >{{ httpOf(c) }}</pre
          >
        </div>
      </div>

      <p v-if="error" class="field-error m-0" role="alert">{{ error }}</p>
      <p v-if="blocked" class="m-0 text-[13px] text-fault-text">
        {{ connection.offline ? 'The gateway does not answer.' : 'A revision waits for confirmation; apply the action after it is resolved.' }}
      </p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || blocked || !valid">{{ busy ? 'Applying…' : 'Apply action' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
