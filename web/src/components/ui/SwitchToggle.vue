<script setup lang="ts">
// Switch (plan §2.17): role="switch" with aria-checked (Reka UI), labelled by its text.
import { SwitchRoot, SwitchThumb } from 'reka-ui';

defineProps<{ label: string; disabled?: boolean }>();
const model = defineModel<boolean>({ required: true });
</script>

<template>
  <label class="inline-flex cursor-pointer items-center gap-2 text-[13px] text-text-2">
    <SwitchRoot
      v-model="model"
      :disabled="disabled"
      :aria-label="label"
      class="relative h-5 w-9 shrink-0 cursor-pointer rounded-full border border-border-strong bg-surface-2 transition-colors data-[state=checked]:border-accent-strong data-[state=checked]:bg-accent-strong disabled:cursor-not-allowed disabled:opacity-50"
    >
      <SwitchThumb
        class="block h-3.5 w-3.5 translate-x-0.5 rounded-full bg-text-2 transition-transform data-[state=checked]:translate-x-[18px] data-[state=checked]:bg-text"
      />
    </SwitchRoot>
    <span aria-hidden="true">{{ label }}</span>
  </label>
</template>
