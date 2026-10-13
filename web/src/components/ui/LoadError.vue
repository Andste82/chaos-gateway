<script setup lang="ts">
// A list or card whose data could not be loaded (plan §2.17, States): it says so with the
// gateway's answer instead of falling through to the empty state, which would claim an answer the
// gateway did not give ("no faults" while the faults are unknown).
import { computed } from 'vue';
import { errorMessage } from '../../api/fetcher';

const props = defineProps<{ error: unknown; what?: string; retry?: () => unknown }>();

const text = computed(() => `${props.what ?? 'Not loaded'}: ${errorMessage(props.error)}.`);
</script>

<template>
  <div
    class="flex flex-wrap items-center gap-3 rounded-[10px] border border-block-border bg-block-tint px-4 py-3 text-sm text-block-text"
    role="alert"
    data-testid="load-error"
  >
    <p class="m-0 min-w-0 flex-1">{{ text }}</p>
    <button v-if="retry" type="button" class="btn btn-sm" @click="retry()">Try again</button>
  </div>
</template>
