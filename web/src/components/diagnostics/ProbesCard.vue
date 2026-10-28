<script setup lang="ts">
// The probes (plan §2.12, §2.17 "Probes"; M25, M26a): the virtual clients of the gateway, each a port of the bridge of a test network
// in a namespace of its own with an address from the network's DHCP. The list shows where each is and whether it has its
// address; a probe is a device, so the faults, rules and profiles that target it act on its traffic. From a probe a
// diagnostic is run, and a profile is measured. A probe of the draft is marked NEW, CHANGED or DELETED until it is applied (M26a):
// Edit opens its dialog, Remove puts its removal into the draft, Undo takes the edit back.
import type { ProbeView } from '../../api/generated/model';
import { refocusAfterSwap } from '../../lib/focus';
import { rowTone, type ProbeRow } from '../../lib/probes';
import { useDraftStore } from '../../stores/draft';
import StatusDot from '../ui/StatusDot.vue';

defineProps<{ rows: ProbeRow[]; networks: Record<string, string>; disabled?: boolean }>();
defineEmits<{ diagnose: [probe: ProbeView]; measure: [probe: ProbeView]; edit: [id: string]; remove: [id: string] }>();
const draft = useDraftStore();

function undo(r: ProbeRow) {
  if (r.mark === 'NEW') draft.setProbe(r.id, null);
  else if (r.mark === 'DELETED') draft.restoreProbe(r.id);
  else draft.revertProbe(r.id);
}
</script>

<template>
  <table class="w-full border-collapse text-sm" data-testid="probes">
    <thead>
      <tr class="text-left text-xs text-text-3">
        <th class="py-1.5 pr-3 font-normal">Probe</th>
        <th class="py-1.5 pr-3 font-normal">Network</th>
        <th class="py-1.5 pr-3 font-normal">Address</th>
        <th class="py-1.5 pr-3 font-normal">MAC</th>
        <th class="py-1.5 pr-3 font-normal">Status</th>
        <th class="py-1.5 font-normal"><span class="sr-only">Actions</span></th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="r in rows" :key="r.id" class="border-t border-border" :class="r.mark === 'DELETED' ? 'opacity-60' : ''" :data-probe="r.config.name">
        <th class="py-2 pr-3 text-left font-medium">
          <RouterLink v-if="r.view" :to="`/devices/${r.id}`" class="text-text hover:underline" :class="r.mark === 'DELETED' ? 'line-through' : ''">{{ r.config.name }}</RouterLink>
          <span v-else>{{ r.config.name }}</span>
          <span v-if="r.config.trusts_test_ca" class="ml-2 text-xs font-normal text-text-3">trusts the test CA</span>
          <span
            v-if="r.mark"
            class="ml-2 rounded border px-1.5 text-xs font-normal"
            :class="r.mark === 'DELETED' ? 'border-block-border text-block-text' : 'border-accent-strong text-accent-text'"
            data-testid="probe-mark"
            >{{ r.mark }}</span
          >
          <p v-if="draft.serverErrors[r.id]" class="m-0 mt-1 text-[13px] font-normal text-block-text" role="alert" data-testid="probe-refused">
            The gateway refused it: {{ Object.values(draft.serverErrors[r.id]!).join(' ') }}
          </p>
        </th>
        <td class="py-2 pr-3 text-text-2">{{ networks[r.config.network] ?? r.config.network }}</td>
        <td class="mono py-2 pr-3">{{ (r.view?.observed.addresses ?? []).join(', ') || '—' }}</td>
        <td class="mono py-2 pr-3 text-text-2">{{ (r.view?.observed.macs ?? [])[0] ?? '—' }}</td>
        <td class="py-2 pr-3"><StatusDot v-bind="rowTone(r)" data-testid="probe-status" /></td>
        <td class="py-2 text-right">
          <span class="inline-flex flex-wrap justify-end gap-2" data-focus-group>
            <template v-if="r.mark === 'DELETED'">
              <button type="button" class="btn btn-sm" :aria-label="`Undo the removal of ${r.config.name}`" data-focus-swap @click="refocusAfterSwap($event); undo(r)">Undo</button>
            </template>
            <template v-else>
              <button
                v-if="r.view"
                type="button"
                class="btn btn-sm"
                :disabled="disabled"
                :aria-label="`Diagnose from ${r.config.name}`"
                @click="$emit('diagnose', r.view)"
              >
                Diagnose from here
              </button>
              <button v-if="r.view" type="button" class="btn btn-sm" :disabled="disabled" :aria-label="`Measure with ${r.config.name}`" @click="$emit('measure', r.view)">Measure…</button>
              <button type="button" class="btn btn-sm" :aria-label="`${draft.serverErrors[r.id] ? 'Fix' : 'Edit'} ${r.config.name}`" @click="$emit('edit', r.id)">
                {{ draft.serverErrors[r.id] ? 'Fix' : 'Edit' }}
              </button>
              <button
                v-if="r.mark !== 'NEW'"
                type="button"
                class="btn btn-sm"
                :aria-label="`Remove ${r.config.name}`"
                @click="$emit('remove', r.id)"
              >
                Remove
              </button>
              <button
                v-if="r.mark"
                type="button"
                class="btn btn-sm"
                :aria-label="r.mark === 'NEW' ? `Take ${r.config.name} out of the draft` : `Undo the changes of ${r.config.name}`"
                data-focus-swap
                @click="refocusAfterSwap($event); undo(r)"
              >
                Undo
              </button>
            </template>
          </span>
        </td>
      </tr>
    </tbody>
  </table>
</template>
