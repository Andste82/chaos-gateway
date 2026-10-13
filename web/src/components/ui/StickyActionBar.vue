<script setup lang="ts">
// Sticky action bar (plan §2.17): at the bottom of the page while something waits for the user,
// e.g. "N changes in M rules · not applied yet" with Discard and Preview & apply.
defineProps<{ label: string }>();
</script>

<template>
  <div
    role="region"
    :aria-label="label"
    class="sticky bottom-0 z-30 mt-auto flex items-center justify-between gap-4 border-t border-accent-strong bg-surface-2 px-6 py-3"
  >
    <div class="text-sm"><slot /></div>
    <div class="flex gap-2"><slot name="actions" /></div>
  </div>
</template>
