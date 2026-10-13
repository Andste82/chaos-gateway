<script setup lang="ts">
// The `</> API` panel (plan §2.17: "every UI action has an API equivalent, and the UI can show it"):
// what the open dialog would send, else the requests of the last action on this screen ("the same
// action from a test"), else what the screen reads, as HTTP or as curl with an API token.
import { computed, ref } from 'vue';
import { curlOf, httpOf, useApiPanelStore } from '../../stores/apiPanel';
import SegmentedControl from '../ui/SegmentedControl.vue';

const store = useApiPanelStore();
const format = ref('http');
const shown = computed(() => store.draft ?? store.last ?? store.context);
const heading = computed(() => (store.draft ? 'This dialog' : store.last ? 'Last action' : 'This screen'));
</script>

<template>
  <section v-if="store.open" class="card flex flex-col gap-3" aria-labelledby="api-panel-title" data-testid="api-panel">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 id="api-panel-title" class="card-title">API · the same from a test</h2>
      <SegmentedControl
        v-model="format"
        label="Format"
        :options="[
          { value: 'http', label: 'HTTP' },
          { value: 'curl', label: 'curl' },
        ]"
      />
    </div>
    <template v-if="shown">
      <p class="m-0 text-[13px] text-text-2">
        {{ heading }}: {{ shown.title }}. Authenticate with an API token (<span class="mono">Authorization: Bearer …</span>; create one with
        <span class="mono">POST /api/v1/auth/tokens</span>).
      </p>
      <div v-for="(c, i) in shown.calls" :key="i" class="flex flex-col gap-1">
        <span v-if="c.note" class="text-xs text-text-3">{{ c.note }}</span>
        <pre
          class="mono m-0 overflow-auto rounded-md border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]"
          data-testid="api-call"
          >{{ format === 'curl' ? curlOf(c) : httpOf(c) }}</pre
        >
      </div>
    </template>
    <p v-else class="m-0 text-sm text-text-2">Nothing on this screen yet.</p>
  </section>
</template>
