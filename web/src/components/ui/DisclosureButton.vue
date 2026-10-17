<script setup lang="ts">
// Disclosure toggle (plan §2.17, issue #63): a text button that shows or hides more options, with a
// triangle in front of its text, ▸ while collapsed and ▾ while expanded. The triangle is decorative
// (the state is aria-expanded) and does not animate; aria-controls names the region it opens.
defineProps<{
  /** The id of the region the button shows and hides. */
  controls: string;
  /** The smaller text of a table row. */
  small?: boolean;
}>();
const open = defineModel<boolean>('open', { required: true });
</script>

<template>
  <button
    type="button"
    class="inline-flex cursor-pointer items-baseline gap-1.5 self-start border-0 bg-transparent p-0 text-left text-accent-text"
    :class="small ? 'text-xs' : 'text-[13px]'"
    :aria-expanded="open"
    :aria-controls="controls"
    @click="open = !open"
  >
    <span aria-hidden="true" class="inline-block w-[1em] shrink-0 text-center" data-testid="disclosure-mark">{{ open ? '▾' : '▸' }}</span>
    <span><slot /></span>
  </button>
</template>
