<script lang="ts">
export interface Segment {
  value: string;
  label: string;
  count?: number;
}
</script>

<script setup lang="ts">
// Segmented control (plan §2.17): one choice of a few, each a real button with aria-pressed. The
// `chips` variant is the row of filter chips of the device list. `describedBy` ties the group to the
// error or hint under it.

defineProps<{ options: Segment[]; label: string; variant?: 'segments' | 'chips'; describedBy?: string }>();
const model = defineModel<string>({ required: true });
</script>

<template>
  <div
    role="group"
    :aria-label="label"
    :aria-describedby="describedBy"
    class="flex"
    :class="variant === 'chips' ? 'gap-1.5' : 'rounded-md border border-border-strong p-0.5'"
  >
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      :aria-pressed="model === o.value"
      class="cursor-pointer text-[13px]"
      :class="[
        variant === 'chips' ? 'h-[34px] rounded-full border px-3' : 'h-[30px] rounded px-3',
        model === o.value
          ? 'border-accent-strong bg-accent-tint text-[#cfe6ff]'
          : variant === 'chips'
            ? 'border-border-strong bg-surface text-text-2 hover:text-text'
            : 'bg-transparent text-text-2 hover:text-text',
      ]"
      @click="model = o.value"
    >
      {{ o.label }}
      <span v-if="o.count !== undefined" class="mono">{{ o.count }}</span>
    </button>
  </div>
</template>
