<script setup lang="ts">
// Full-width banner at the top of the page (plan §2.17): `offline` when the backend does not
// answer, `safe-mode` (red) when the gateway runs without forwarding. role="alert" so it is
// announced when it appears.
import AppIcon from './AppIcon.vue';

defineProps<{ kind: 'offline' | 'safe-mode'; title: string }>();
</script>

<template>
  <div
    role="alert"
    class="flex items-center gap-3 border-b px-6 py-3 text-sm"
    :class="
      kind === 'safe-mode'
        ? 'border-block-border bg-block-tint text-block-text'
        : 'border-border-strong bg-surface-2 text-text'
    "
    :data-testid="`banner-${kind}`"
  >
    <AppIcon :name="kind === 'safe-mode' ? 'warning' : 'offline'" />
    <strong class="font-semibold">{{ title }}</strong>
    <span class="text-[13px] opacity-90"><slot /></span>
  </div>
</template>
