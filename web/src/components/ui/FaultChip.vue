<script setup lang="ts">
// Fault chip (plan §2.17): mono, orange; anything that impairs traffic. A long text is cut with an
// ellipsis and shown whole on hover.
defineProps<{ text: string }>();
</script>

<template>
  <span
    class="mono inline-block max-w-full truncate rounded border border-fault-border bg-fault-tint px-2 py-0.5 align-middle text-xs whitespace-nowrap text-fault-text"
    :title="text"
    >{{ text }}</span
  >
</template>
