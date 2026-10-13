<script setup lang="ts">
// Empty state (plan §2.17): a dashed box with one sentence and the primary action.
defineProps<{ text: string }>();
</script>

<template>
  <div
    class="flex flex-col items-center gap-3 rounded-[10px] border border-dashed border-border-strong px-4 py-8 text-center text-sm text-text-2"
    data-testid="empty-state"
  >
    <p class="m-0">{{ text }}</p>
    <div v-if="$slots.default"><slot /></div>
  </div>
</template>
