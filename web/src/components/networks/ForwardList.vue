<script setup lang="ts">
// The port forwards of one entry side on its card of the networks view (plan §2.17 "Networks", M35a):
// protocol, external port or range, target, target port, enabled, the counter and the status ("inactive:
// the device has no address"); a forward of the draft is marked NEW, CHANGED or DELETED until it is
// applied. Edit opens the form; Disable, Enable and Delete change the draft, which goes through the
// unapplied-changes bar and the preview-and-apply drawer; Undo takes the edit back.
import { refocusAfterSwap } from '../../lib/focus';
import { counterText, forwardSummary, forwardTitle, rowStatus, type DeviceName, type ForwardRow } from '../../lib/forwards';
import { useDraftStore } from '../../stores/draft';
import StatusDot from '../ui/StatusDot.vue';

defineProps<{ rows: ForwardRow[]; deviceName: DeviceName; label: string }>();
const emit = defineEmits<{ edit: [id: string] }>();
const draft = useDraftStore();

function undo(r: ForwardRow) {
  if (r.mark === 'NEW') draft.setForward(r.id, null);
  else draft.restoreForward(r.id);
}
function setEnabled(r: ForwardRow, on: boolean) {
  const p = { ...r.p };
  if (on) delete p.enabled;
  else p.enabled = false;
  // a forward the base has enabled explicitly keeps that
  if (on && draft.baseForwards[r.id]?.enabled === true) p.enabled = true;
  draft.setForward(r.id, p);
}
</script>

<template>
  <ul class="m-0 flex w-full list-none flex-col gap-1.5 p-0" :aria-label="label" data-testid="forwards">
    <li
      v-for="r in rows"
      :key="r.id"
      class="rounded-md border border-[#2a323d] bg-surface-2 px-2.5 py-2 text-[13px]"
      :class="r.mark === 'DELETED' ? 'opacity-60' : ''"
      data-testid="forward"
    >
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="mono" :class="r.mark === 'DELETED' ? 'line-through' : ''" data-testid="forward-summary">{{ forwardSummary(r.p, deviceName) }}</span>
        <span v-if="r.p.name" class="text-text-2">{{ r.p.name }}</span>
        <span
          v-if="r.mark"
          class="rounded border px-1.5 text-xs"
          :class="r.mark === 'DELETED' ? 'border-block-border text-block-text' : 'border-accent-strong text-accent-text'"
          data-testid="forward-mark"
          >{{ r.mark }}</span
        >
      </div>
      <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
        <StatusDot v-bind="rowStatus(r)" data-testid="forward-status" />
        <span v-if="counterText(r)" class="mono text-xs text-text-2" data-testid="forward-counter">{{ counterText(r) }}</span>
      </div>
      <p v-if="draft.serverErrors[r.id]" class="m-0 mt-1 text-[13px] text-block-text" role="alert" data-testid="forward-refused">
        The gateway refused it: {{ Object.values(draft.serverErrors[r.id]!).join(' ') }}
      </p>
      <div class="mt-1.5 flex flex-wrap gap-1.5" data-focus-group>
        <template v-if="r.mark === 'DELETED'">
          <button
            type="button"
            class="btn btn-sm"
            :aria-label="`Undo the deletion of ${forwardTitle(r.p)}`"
            data-focus-swap
            @click="
              refocusAfterSwap($event);
              undo(r);
            "
          >
            Undo
          </button>
        </template>
        <template v-else>
          <button type="button" class="btn btn-sm" :aria-label="`${draft.serverErrors[r.id] ? 'Fix' : 'Edit'} ${forwardTitle(r.p)}`" @click="emit('edit', r.id)">
            {{ draft.serverErrors[r.id] ? 'Fix' : 'Edit' }}
          </button>
          <button type="button" class="btn btn-sm" :aria-label="`${r.p.enabled === false ? 'Enable' : 'Disable'} ${forwardTitle(r.p)}`" @click="setEnabled(r, r.p.enabled === false)">
            {{ r.p.enabled === false ? 'Enable' : 'Disable' }}
          </button>
          <button
            type="button"
            class="btn btn-sm"
            :aria-label="`Delete ${forwardTitle(r.p)}`"
            @click="
              refocusAfterSwap($event);
              draft.setForward(r.id, null);
            "
          >
            Delete
          </button>
          <button
            v-if="r.mark"
            type="button"
            class="btn btn-sm"
            :aria-label="`Undo the changes of ${forwardTitle(r.p)}`"
            @click="
              refocusAfterSwap($event);
              undo(r);
            "
          >
            Undo
          </button>
        </template>
      </div>
    </li>
  </ul>
</template>
