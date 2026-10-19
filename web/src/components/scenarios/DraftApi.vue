<script setup lang="ts">
// The `</> API` part of an editor dialog (plan §2.17: every UI action has an API equivalent, and the UI can
// show it): what "Add to the changes" leads to, the merge patch the preview sends, as HTTP. The dialog puts it
// in the API panel's `draft` (`useDraftApi`).
import { ref, useId } from 'vue';
import { httpOf, useApiPanelStore } from '../../stores/apiPanel';
import DisclosureButton from '../ui/DisclosureButton.vue';

defineProps<{ /** What the requests are for: "the preview sends" (the default) or "Run sends". */ what?: string }>();
const apiPanel = useApiPanelStore();
const open = ref(false);
const uid = useId();
</script>

<template>
  <div v-if="apiPanel.draft" class="flex flex-col" data-testid="scenario-api">
    <DisclosureButton v-model:open="open" :controls="`${uid}-api`">&lt;/&gt; API · what {{ what ?? 'the preview' }} sends</DisclosureButton>
    <div v-if="open" :id="`${uid}-api`">
      <pre
        v-for="(c, i) in apiPanel.draft.calls"
        :key="i"
        class="mono m-0 mt-2 max-h-48 overflow-auto rounded-md border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre-wrap text-[#d9dee5]"
        data-testid="api-call"
        >{{ httpOf(c) }}</pre
      >
    </div>
  </div>
</template>
